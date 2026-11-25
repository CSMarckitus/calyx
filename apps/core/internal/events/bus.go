// Package events moves what happens in the core to whoever listens: webhooks and gRPC streams.
package events

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID           string
	InstanceName string
	Type         string
	Payload      any
	OccurredAt   time.Time
}

// Bus is the single place every event of the core goes through. Producers (instances, bulk jobs)
// publish, consumers (webhooks, gRPC streams) subscribe; neither side knows about the other.
//
// Listeners are called synchronously from Publish and must not block: anything slow (an HTTP
// request, a client stream) has to be handed to another goroutine.
type Bus struct {
	mu        sync.Mutex
	nextID    int
	listeners map[int]func(Event)
}

func NewBus() *Bus {
	return &Bus{listeners: map[int]func(Event){}}
}

func (b *Bus) Publish(instanceName, eventType string, payload any) {
	b.mu.Lock()
	listeners := make([]func(Event), 0, len(b.listeners))
	for _, listener := range b.listeners {
		listeners = append(listeners, listener)
	}
	b.mu.Unlock()
	if len(listeners) == 0 {
		return
	}

	event := Event{
		ID:           uuid.NewString(),
		InstanceName: instanceName,
		Type:         eventType,
		Payload:      payload,
		OccurredAt:   time.Now(),
	}
	// Called without the lock, so a listener may subscribe or unsubscribe.
	for _, listener := range listeners {
		listener(event)
	}
}

// Subscribe returns the function to call to stop listening.
func (b *Bus) Subscribe(listener func(Event)) (unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	b.listeners[id] = listener
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.listeners, id)
	}
}

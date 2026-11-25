package events

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Enqueuer hands a delivery to a queue that survives restarts (asynq on Redis).
type Enqueuer interface {
	Enqueue(ctx context.Context, webhookID string, event SerializedEvent) error
}

type DispatcherOptions struct {
	// When set, deliveries go to the queue instead of being sent from this process.
	Enqueuer Enqueuer
	// Waits between direct attempts. Default 1s, 2s, 3s.
	RetryDelays []time.Duration
	Client      *http.Client
}

type webhook struct {
	id, eventType, url, secret string
}

// Dispatcher turns the events of the bus into webhook deliveries. With an Enqueuer they go
// through Redis; otherwise they are sent from this process with a few retries.
type Dispatcher struct {
	db   *sql.DB
	bus  *Bus
	opts DispatcherOptions

	mu          sync.Mutex
	cache       map[string][]webhook // active webhooks per instance name
	generation  map[string]int       // bumped by Invalidate, so a load that raced with it is not cached
	unsubscribe func()

	inFlight sync.WaitGroup
	stop     chan struct{}
	stopOnce sync.Once
}

func NewDispatcher(db *sql.DB, bus *Bus, opts DispatcherOptions) *Dispatcher {
	if opts.RetryDelays == nil {
		opts.RetryDelays = []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	return &Dispatcher{
		db:         db,
		bus:        bus,
		opts:       opts,
		cache:      map[string][]webhook{},
		generation: map[string]int{},
		stop:       make(chan struct{}),
	}
}

func (d *Dispatcher) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.unsubscribe != nil {
		return
	}
	d.unsubscribe = d.bus.Subscribe(func(e Event) {
		// The bus must not block: every event is handled in its own goroutine.
		d.inFlight.Add(1)
		go func() {
			defer d.inFlight.Done()
			d.dispatch(e)
		}()
	})
}

// Stop stops listening, cuts the waits between retries and waits for the requests in flight.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if d.unsubscribe != nil {
		d.unsubscribe()
		d.unsubscribe = nil
	}
	d.mu.Unlock()
	d.stopOnce.Do(func() { close(d.stop) })
	d.inFlight.Wait()
}

// Invalidate must be called whenever the webhooks of an instance change, or the instance is
// renamed or removed.
func (d *Dispatcher) Invalidate(instanceName string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.cache, instanceName)
	d.generation[instanceName]++
}

func (d *Dispatcher) webhooksOf(instanceName string) ([]webhook, error) {
	d.mu.Lock()
	cached, ok := d.cache[instanceName]
	generation := d.generation[instanceName]
	d.mu.Unlock()
	if ok {
		return cached, nil
	}

	rows, err := d.db.Query(`SELECT w.id, w.event_type, w.url, COALESCE(w.secret, '') FROM webhooks w
		JOIN instances i ON i.id = w.instance_id WHERE i.name = ? AND w.is_active = 1`, instanceName)
	if err != nil {
		return nil, err // a failed query is not remembered
	}
	defer rows.Close()
	var loaded []webhook
	for rows.Next() {
		var w webhook
		if err := rows.Scan(&w.id, &w.eventType, &w.url, &w.secret); err != nil {
			return nil, err
		}
		loaded = append(loaded, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.mu.Lock()
	if d.generation[instanceName] == generation {
		d.cache[instanceName] = loaded
	}
	d.mu.Unlock()
	return loaded, nil
}

func (d *Dispatcher) dispatch(e Event) {
	log := slog.With("instance", e.InstanceName, "event", e.Type)
	webhooks, err := d.webhooksOf(e.InstanceName)
	if err != nil {
		log.Error("Could not read the webhooks of the instance.", "err", err)
		return
	}

	var serialized *SerializedEvent
	for _, w := range webhooks {
		if w.eventType != e.Type {
			continue
		}
		if serialized == nil {
			s, err := Serialize(e)
			if err != nil {
				log.Error("Could not serialize the event.", "err", err)
				return
			}
			serialized = &s
		}
		if d.opts.Enqueuer != nil {
			if err := d.opts.Enqueuer.Enqueue(context.Background(), w.id, *serialized); err != nil {
				log.Error("Could not queue the webhook.", "url", w.url, "err", err)
			}
			continue
		}
		d.deliverDirectly(log, w, *serialized)
	}
}

func (d *Dispatcher) deliverDirectly(log *slog.Logger, w webhook, event SerializedEvent) {
	for attempt := 0; ; attempt++ {
		err := Send(context.Background(), d.opts.Client, w.url, w.secret, event)
		if err == nil {
			return
		}
		if attempt >= len(d.opts.RetryDelays) || !IsWorthRetrying(err) {
			log.Error("Webhook delivery failed.", "url", w.url, "attempts", attempt+1, "err", err)
			return
		}
		select {
		case <-time.After(d.opts.RetryDelays[attempt]):
		case <-d.stop:
			log.Warn("Shutting down: webhook delivery abandoned.", "url", w.url)
			return
		}
	}
}

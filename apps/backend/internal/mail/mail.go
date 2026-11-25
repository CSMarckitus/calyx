// Package mail holds the mail seam (Sender), the templates and Render.
package mail

import (
	"context"
	"errors"
)

type Message struct{ To, Subject, Body string }

// Sender sends one mail. It returns ErrNotConfigured when mail is off.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

var ErrNotConfigured = errors.New("mail is not configured")

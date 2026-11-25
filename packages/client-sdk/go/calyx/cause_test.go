package calyx

import (
	"context"
	"errors"
	"testing"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/internal/errcause"
)

func TestErrcauseSetSetsTheCause(t *testing.T) {
	err := &Error{Source: SourceClient, Code: CodeAborted}
	errcause.Set(err, context.Canceled)
	if !errors.Is(err.cause, context.Canceled) {
		t.Fatalf("cause = %v, want context.Canceled", err.cause)
	}
	if !errors.Is(err, context.Canceled) {
		t.Error("errors.Is does not reach the cause")
	}
}

func TestErrcauseSetIgnoresOtherErrors(t *testing.T) {
	// Another error type must not make Set panic.
	errcause.Set(errors.New("x"), context.Canceled)
}

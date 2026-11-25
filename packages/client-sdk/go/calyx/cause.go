package calyx

import (
	"errors"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/internal/errcause"
)

// The other packages of the SDK set the cause through errcause, so calyx needs no exported
// setter.
func init() {
	errcause.Set = func(err, cause error) {
		var e *Error
		if errors.As(err, &e) {
			e.cause = cause
		}
	}
}

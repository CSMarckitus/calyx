package instances

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/tenancy"
)

// Send sends one text message. It never retries: a send whose outcome is unknown answers
// 504 send_outcome_unknown. It logs nothing; the pacing is the core's.
func (s *Service) Send(ctx context.Context, p tenancy.Principal, id uuid.UUID, to, text, key string) (core.SendResult, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return core.SendResult{}, err
	}
	if err := CheckIdempotencyKey(key); err != nil {
		return core.SendResult{}, err
	}
	var f apierr.Fields
	CheckRecipient(&f, "to", to)
	CheckText(&f, "text", text)
	if err := f.Err(); err != nil {
		return core.SendResult{}, err
	}
	to = strings.TrimSpace(to)
	row, err := s.load(ctx, p, id)
	if err != nil {
		return core.SendResult{}, err
	}
	if row.State != "active" {
		// The core has no instance to check, so no status check starts.
		return core.SendResult{}, apierr.Conflict(apierr.CodeInstanceNotConnected, "The instance is not ready to send. Its state is "+row.State+".")
	}
	st, err := s.core.GetInstanceStatus(ctx, row.CoreName)
	if err != nil {
		return core.SendResult{}, err
	}
	if st != core.StatusConnected && key == "" {
		s.TriggerStatusCheck(ctx, row.ID)
		return core.SendResult{}, apierr.Conflict(apierr.CodeInstanceNotConnected, fmt.Sprintf("The instance is not connected; its status is %s.", st))
	}
	// A keyed send reaches the core also when the instance is not connected: the core answers a
	// known key and refuses a new send with UNAVAILABLE.
	res, err := s.core.SendText(ctx, row.CoreName, to, text, key)
	if err != nil {
		if st != core.StatusConnected && core.IsUnavailable(err) {
			s.TriggerStatusCheck(ctx, row.ID)
			return core.SendResult{}, apierr.Conflict(apierr.CodeInstanceNotConnected, "The instance is not connected.")
		}
		if isCode(err, apierr.CodeInstanceNotReady) {
			s.TriggerStatusCheck(ctx, row.ID)
		}
		return core.SendResult{}, err
	}
	return res, nil
}

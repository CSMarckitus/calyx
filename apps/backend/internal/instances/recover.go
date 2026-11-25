package instances

import (
	"context"
	"time"

	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
)

// recoverMargin is added to the pairing timeout. A "creating" row without a session is recovered
// only after that age.
const recoverMargin = time.Minute

var knownStatuses = map[string]bool{
	core.StatusConnected: true, core.StatusConnecting: true, core.StatusDisconnected: true,
	core.StatusNeedsAuth: true, core.StatusError: true,
}

// Recover finishes the rows that a stop or a crash interrupted. coreInstances is the list that
// the core reported just now. A "creating" row without a session becomes active when the core has
// the instance, and is deleted when it does not. A "removing" row gets its core call again. A core
// failure on one row never stops the others; only a database failure returns an error.
func (s *Service) Recover(ctx context.Context, coreInstances []core.InstanceInfo) error {
	q := gen.New(s.pool)
	now := s.clk.Now()
	rows, err := q.ListRecoverableInstances(ctx, gen.ListRecoverableInstancesParams{
		CoreID: s.coreID, CreatingBefore: now.Add(-(s.pairingTimeout + recoverMargin)),
	})
	if err != nil {
		return err
	}
	known := make(map[string]core.InstanceInfo, len(coreInstances))
	for _, in := range coreInstances {
		known[in.CoreName] = in
	}
	for _, row := range rows {
		switch row.State {
		case "creating":
			if s.pairings.Running(row.ID) {
				continue
			}
			info, ok := known[row.CoreName]
			switch {
			case !ok:
				if _, err := q.DeleteCreatingInstance(ctx, row.ID); err != nil {
					return err
				}
				s.published(row)
			case !knownStatuses[info.Status]:
				s.log.Warn("The core reported an unknown status.", "instance_id", row.ID, "status", info.Status)
			default:
				if _, err := q.ActivateRecoveredInstance(ctx, gen.ActivateRecoveredInstanceParams{
					ID: row.ID, LastStatus: info.Status, Now: now,
				}); err != nil {
					return err
				}
				if err := q.SetInstanceIdentity(ctx, gen.SetInstanceIdentityParams{
					ID: row.ID, DeviceIdentity: nilIfEmpty(info.DeviceIdentity), Locale: nilIfEmpty(info.Locale),
				}); err != nil {
					return err
				}
				s.published(row)
			}
		case "removing":
			if err := s.core.RemoveInstance(ctx, row.CoreName); err != nil {
				s.log.Warn("Cannot remove an instance from the core yet.", "instance_id", row.ID, "error", err)
				continue
			}
			if err := q.DeleteInstance(ctx, row.ID); err != nil {
				return err
			}
			s.published(row)
		}
	}
	return nil
}

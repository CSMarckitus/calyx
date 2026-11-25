package engine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/core/internal/database"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/testutil"
)

// BenchmarkSingleSend is the baseline of the single send path: one connected instance, 200 sends
// one at a time, each to a different recipient. Run it with -benchtime 1x.
func BenchmarkSingleSend(b *testing.B) {
	const sends = 200
	ctx := context.Background()
	for range b.N {
		b.StopTimer()
		db, err := database.Open(ctx, "file:"+filepath.Join(b.TempDir(), "bench.sqlite"))
		if err != nil {
			b.Fatal(err)
		}
		now := time.Now().UnixMilli()
		if _, err := db.Exec(`INSERT INTO instances (id, name, provider, connection_status, created_at, updated_at)
			VALUES (?, 'shop', 'WHATSAPP_WEB', 'CONNECTED', ?, ?)`, uuid.NewString(), now, now); err != nil {
			b.Fatal(err)
		}
		o := engine.NewOrchestrator(db, engine.NewBulkStore(db), events.NewBus(), engine.OrchestratorOptions{
			NormalizeRecipient: testutil.NormalizeRecipient,
			BuildProvider: func(row engine.InstanceRow, hooks engine.ProviderHooks) (engine.Provider, error) {
				fake := testutil.NewFakeProvider(row.Name)
				fake.SetHooks(hooks)
				return fake, nil
			},
		})
		b.StartTimer()
		for i := range sends {
			if _, err := o.SendText(ctx, "shop", fmt.Sprintf("3460000%04d", i), "hello", nil); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		o.DestroyAllSessions()
		db.Close()
	}
}

package engine_test

// Between the CONNECTED write of a pairing and its register,
// nothing may open a second session of the same device.

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/testutil"
)

// countedFixture counts the connections of every fake that the fixture builds.
func countedFixture(t *testing.T) (*orchestratorFixture, *testutil.ConnectionCounter) {
	t.Helper()
	f := newOrchestratorFixture(t)
	counter := testutil.NewConnectionCounter()
	f.configure = func(fake *testutil.FakeProvider) { fake.CountConnections(counter) }
	return f, counter
}

func TestACallAtTheEndOfAPairingOpensNoSecondSession(t *testing.T) {
	calls := map[string]func(o *engine.Orchestrator) error{
		"SendText": func(o *engine.Orchestrator) error {
			_, err := o.SendText(ctx, "shop", "34600000001", "hi", nil)
			return err
		},
		"CreateBulkJob": func(o *engine.Orchestrator) error {
			_, _, err := o.CreateBulkJob(ctx, "shop", engine.BulkCustom, bulkOf("34600000001"), engine.BulkPacingOptions{}, nil)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			f, counter := countedFixture(t)
			f.instance(t, "shop", "is_used_only_for_send_messages = 1, connection_status = 'NEEDS_AUTH'")
			var callErr error
			engine.SetPairedHook(f.o, func() { callErr = call(f.o) })

			if err := f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {}); err != nil {
				t.Fatal(err)
			}
			if calyxerr.CodeOf(callErr) != codes.FailedPrecondition || !strings.Contains(callErr.Error(), "is being paired") {
				t.Errorf("the call during the pairing got %v, want FailedPrecondition", callErr)
			}
			if got := counter.Max("shop"); got != 1 {
				t.Errorf("%d sessions open at the same time, want 1", got)
			}
			f.o.DestroyAllSessions()
			if got := counter.Open("shop"); got != 0 {
				t.Errorf("%d sessions still open after DestroyAllSessions", got)
			}
		})
	}
}

func TestRegisterKeepsTheLiveInstance(t *testing.T) {
	f := newOrchestratorFixture(t)
	logs := captureLogs(t)
	f.instance(t, "shop", "")
	f.o.LoadAllActiveInstances(ctx)
	first := f.fake("shop")

	second := testutil.NewFakeProvider("shop")
	if err := engine.Register(f.o, f.builtRow("shop"), second); err != nil {
		t.Fatal(err)
	}

	if _, destroys, _ := second.Counts(); destroys != 1 {
		t.Errorf("the second provider was destroyed %d times, want 1", destroys)
	}
	if !strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("no ERROR line in the logs:\n%s", logs)
	}
	if _, err := f.o.SendText(ctx, "shop", "34600000001", "hi", nil); err != nil {
		t.Fatal(err)
	}
	if len(first.Sent()) != 1 || len(second.Sent()) != 0 {
		t.Errorf("first sent %d, second sent %d: the live instance changed", len(first.Sent()), len(second.Sent()))
	}
}

func TestTheWakeSkipsAnInstanceThatIsBeingPaired(t *testing.T) {
	f, counter := countedFixture(t)
	logs := captureLogs(t)
	f.instance(t, "shop", "is_used_only_for_send_messages = 1, connection_status = 'NEEDS_AUTH'")
	clock := f.startWakeRetry(t)
	engine.SetPairedHook(f.o, func() {
		// The status that a wake read before the pairing changed the row.
		if _, err := f.db.Exec("UPDATE instances SET connection_status = 'DISCONNECTED' WHERE name = 'shop'"); err != nil {
			t.Fatal(err)
		}
		clock.run(t)
		if _, err := f.db.Exec("UPDATE instances SET connection_status = 'CONNECTED' WHERE name = 'shop'"); err != nil {
			t.Fatal(err)
		}
	})

	if err := f.o.CreateInstance(ctx, newPairing("shop"), func(engine.CreateInstanceUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if got := counter.Max("shop"); got != 1 {
		t.Errorf("%d sessions open at the same time, want 1", got)
	}
	if strings.Contains(logs.String(), wakeWarn) {
		t.Errorf("the wake tried the instance during its pairing:\n%s", logs)
	}
}

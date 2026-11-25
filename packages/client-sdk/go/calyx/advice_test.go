package calyx_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

type adviceRow struct {
	Source string `json:"source"`
	Code   string `json:"code"`
	Retry  string `json:"retry"`
}

func readAdviceRows(t *testing.T) []adviceRow {
	t.Helper()
	// A missing file fails the test: the shared table must always be reachable.
	raw, err := os.ReadFile("../../testdata/retry-advice.json")
	if err != nil {
		t.Fatalf("read retry-advice.json: %v", err)
	}
	var doc struct {
		Rows []adviceRow `json:"rows"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse retry-advice.json: %v", err)
	}
	return doc.Rows
}

func TestAdviceForMatchesSharedTable(t *testing.T) {
	rows := readAdviceRows(t)
	if len(rows) != 65 {
		t.Fatalf("rows = %d, want 65", len(rows))
	}
	for _, r := range rows {
		got := calyx.AdviceFor(calyx.Source(r.Source), calyx.Code(r.Code))
		if string(got) != r.Retry {
			t.Errorf("AdviceFor(%s, %s) = %q, want %q", r.Source, r.Code, got, r.Retry)
		}
	}
}

// Every code constant of the SDK must have a row in the shared table.
func TestSharedTableCoversEveryCodeConstant(t *testing.T) {
	inTable := map[string]bool{}
	for _, r := range readAdviceRows(t) {
		inTable[r.Source+"/"+r.Code] = true
	}
	check := func(source calyx.Source, codes []calyx.Code) {
		for _, c := range codes {
			if !inTable[string(source)+"/"+string(c)] {
				t.Errorf("no row for %s %s", source, c)
			}
		}
	}
	check(calyx.SourceBackend, backendCodes)
	check(calyx.SourceCore, append(append([]calyx.Code{}, coreReasonCodes...), grpcCodes...))
	check(calyx.SourceClient, clientCodes)
}

func TestAdviceForUnknownCode(t *testing.T) {
	if got := calyx.AdviceFor(calyx.SourceBackend, "a_new_code"); got != calyx.RetryUnknown {
		t.Errorf("unknown code = %q, want unknown", got)
	}
	if got := calyx.AdviceFor(calyx.SourceCore, "not_found"); got != calyx.RetryUnknown {
		t.Errorf("code of the wrong source = %q, want unknown", got)
	}
}

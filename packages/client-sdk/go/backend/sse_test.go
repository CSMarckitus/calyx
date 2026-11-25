package backend

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// chunkReader gives one chunk per Read, like a network stream.
type chunkReader struct{ chunks []string }

func (c *chunkReader) Read(b []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	if c.chunks[0] == "" {
		c.chunks = c.chunks[1:]
	}
	return n, nil
}

type sseCase struct {
	Name     string   `json:"name"`
	Chunks   []string `json:"chunks"`
	Expected struct {
		Items []struct {
			Event string `json:"event"`
			Data  any    `json:"data"`
		} `json:"items"`
		Error *string `json:"error"`
	} `json:"expected"`
}

func TestSSECases(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/sse-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []sseCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			p := newParser(&chunkReader{chunks: tc.Chunks})
			got := []any{}
			var gotErr error
			// The loop mirrors FollowPairing: done and failed end the stream.
			for {
				event, data, err := p.nextItem()
				if err != nil {
					gotErr = err
					break
				}
				var v any
				if err := json.Unmarshal(data, &v); err != nil {
					t.Fatal(err)
				}
				got = append(got, map[string]any{"event": event, "data": v})
				if event != "update" {
					break
				}
			}
			want := []any{}
			for _, it := range tc.Expected.Items {
				want = append(want, map[string]any{"event": it.Event, "data": it.Data})
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("items = %v, want %v", got, want)
			}
			if tc.Expected.Error == nil {
				if gotErr != nil {
					t.Errorf("error = %v, want none", gotErr)
				}
				return
			}
			var e *calyx.Error
			if !errors.As(gotErr, &e) || string(e.Code) != *tc.Expected.Error {
				t.Errorf("error = %v, want code %s", gotErr, *tc.Expected.Error)
			}
			if e != nil && (e.Source != calyx.SourceClient || e.Retry == "") {
				t.Errorf("source = %q, retry = %q", e.Source, e.Retry)
			}
		})
	}
}

func TestSSELongLine(t *testing.T) {
	long := strings.Repeat("a", 200*1024)
	p := newParser(bytes.NewReader([]byte("event: update\ndata: \"" + long + "\"\n\n")))
	f, err := p.next()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.data) != len(long)+2 {
		t.Errorf("data length = %d", len(f.data))
	}
}

func TestSSEReadError(t *testing.T) {
	boom := errors.New("boom")
	p := newParser(io.MultiReader(strings.NewReader("event: update\n"), errReader{boom}))
	if _, err := p.next(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// setMaxEvent sets a small event size limit for one test.
func setMaxEvent(t *testing.T, n int) {
	t.Helper()
	old := maxEventBytes
	maxEventBytes = n
	t.Cleanup(func() { maxEventBytes = old })
}

func TestSSEEventOverTheLimitGivesInvalidResponse(t *testing.T) {
	setMaxEvent(t, 1024)
	// Many short lines: no single line is long, but the event passes 1 KiB.
	event := "event: update\n" + strings.Repeat("data: 1\n", 200) + "\n"
	p := newParser(strings.NewReader(event))
	_, _, err := p.nextItem()
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

func TestSSELimitCountsCommentsAndResetsAtEmptyLine(t *testing.T) {
	setMaxEvent(t, 100)
	ping := ": ping\n\n"
	// Each event is under the limit. The whole stream is over it.
	stream := strings.Repeat(ping, 50) + "event: done\ndata: {}\n\n"
	p := newParser(strings.NewReader(stream))
	if _, _, err := p.nextItem(); err != nil {
		t.Fatalf("pings that end with an empty line must reset the count: %v", err)
	}
	// A comment line that never ends an event also counts.
	p = newParser(strings.NewReader(strings.Repeat(": ping\n", 50)))
	_, _, err := p.nextItem()
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

func TestSSELimitStopsAGrowingLine(t *testing.T) {
	setMaxEvent(t, 1024)
	// The line has no end: the parser must stop while it reads, not at the end of the stream.
	p := newParser(strings.NewReader("data: " + strings.Repeat("a", 1<<20)))
	_, _, err := p.nextItem()
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

// sizedEvent returns a done event of exactly n bytes, terminators and the empty line included.
func sizedEvent(n int) string {
	const base = "event: done\ndata: {}\n\n" // 22 bytes
	pad := n - len(base) - len(": \n")
	return "event: done\ndata: {}\n" + ": " + strings.Repeat("a", pad) + "\n\n"
}

func TestSSEEventAtTheLimitIsAccepted(t *testing.T) {
	setMaxEvent(t, 100)
	event := sizedEvent(100)
	if len(event) != 100 {
		t.Fatalf("test event has %d bytes, want 100", len(event))
	}
	if _, _, err := newParser(strings.NewReader(event)).nextItem(); err != nil {
		t.Fatalf("an event of exactly the limit must pass: %v", err)
	}
	event = sizedEvent(101)
	_, _, err := newParser(strings.NewReader(event)).nextItem()
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

func TestSSELimitWithCRLFResetsAtEmptyLine(t *testing.T) {
	setMaxEvent(t, 100)
	stream := strings.Repeat(": ping\r\n\r\n", 50) + "event: done\r\ndata: {}\r\n\r\n"
	if _, _, err := newParser(strings.NewReader(stream)).nextItem(); err != nil {
		t.Fatalf("CRLF pings that end with an empty line must reset the count: %v", err)
	}
}

func TestSSELimitCountsACRLFSplitAcrossReads(t *testing.T) {
	setMaxEvent(t, 100)
	crlfEvent := func(pad int) string {
		return "event: done\r\ndata: {}\r\n: " + strings.Repeat("p", pad) + "\r\n\r\n"
	}
	event := crlfEvent(71)
	if len(event) != 100 {
		t.Fatalf("test event has %d bytes, want 100", len(event))
	}
	for name, r := range map[string]io.Reader{
		"whole": strings.NewReader(event),
		"split": iotest.OneByteReader(strings.NewReader(event)),
	} {
		if _, _, err := newParser(r).nextItem(); err != nil {
			t.Fatalf("%s read of an event at the limit: %v", name, err)
		}
	}
	// The parser ends the event at the "\r" of the empty line. The "\n" after it counts for the
	// next event, so the CRLF event is over the limit when that "\r" is byte 101.
	over := crlfEvent(73)
	_, _, err := newParser(iotest.OneByteReader(strings.NewReader(over))).nextItem()
	_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
}

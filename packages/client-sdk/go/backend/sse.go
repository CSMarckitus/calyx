package backend

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// frame is one server-sent event: the event name and the data lines joined with "\n".
type frame struct {
	event string
	data  string
}

// maxEventBytes is the largest event that the parser accepts (1 MiB). It is a variable so that
// the tests can make it small.
var maxEventBytes = 1 << 20

// parser turns a byte stream into frames. It reads with a bufio.Reader, because a bufio.Scanner
// refuses a line longer than 64 KiB.
type parser struct {
	r *bufio.Reader
	// afterCR is true when the last line ended with "\r". A "\n" that follows belongs to it.
	afterCR bool
	// size is the number of bytes read for the event now, line terminators included.
	size int
}

func newParser(r io.Reader) *parser { return &parser{r: bufio.NewReader(r)} }

// line reads one line. A line ends with "\n", "\r" or "\r\n". It returns the read error, and
// drops a partial line at the end of the stream.
func (p *parser) line() (string, error) {
	var sb strings.Builder
	for {
		b, err := p.r.ReadByte()
		if err != nil {
			return "", err
		}
		// The check runs while the line grows, so a line without an end cannot fill the memory.
		if p.size++; p.size > maxEventBytes {
			return "", streamError(calyx.CodeInvalidResponse, "The backend sent an event that is too large.")
		}
		if b == '\n' && p.afterCR && sb.Len() == 0 {
			// The "\r\n" split between two reads: skip the "\n" of the line that already ended.
			p.afterCR = false
			continue
		}
		p.afterCR = false
		switch b {
		case '\n':
			return sb.String(), nil
		case '\r':
			// Do not peek: the next byte can wait for a long time.
			p.afterCR = true
			return sb.String(), nil
		}
		sb.WriteByte(b)
	}
}

// next returns the next frame that has data. A frame ends at an empty line. Comments (pings
// too) and frames without data give nothing. At the end of the stream it returns io.EOF.
func (p *parser) next() (frame, error) {
	var event string
	var data []string
	for {
		l, err := p.line()
		if err != nil {
			return frame{}, err
		}
		if l == "" {
			p.size = 0
			if len(data) > 0 {
				return frame{event: event, data: strings.Join(data, "\n")}, nil
			}
			event = ""
			continue
		}
		if l[0] == ':' {
			continue
		}
		field, value, _ := strings.Cut(l, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
}

// nextItem returns the next frame of a known event ("update", "done" or "failed") with its data
// checked as JSON. It skips other events. The end of the stream gives stream_failed and data
// that is not JSON gives invalid_response. FollowPairing stops after "done" or "failed". A read
// error comes back as it is, so the caller can tell a cancel or an idle timeout from a break.
func (p *parser) nextItem() (string, json.RawMessage, error) {
	for {
		f, err := p.next()
		if errors.Is(err, io.EOF) {
			return "", nil, streamError(calyx.CodeStreamFailed, "The pairing stream ended before done or failed.")
		}
		if err != nil {
			return "", nil, err
		}
		if f.event != "update" && f.event != "done" && f.event != "failed" {
			continue
		}
		if !json.Valid([]byte(f.data)) {
			return "", nil, streamError(calyx.CodeInvalidResponse,
				"The backend sent a \""+f.event+"\" event with data that is not JSON.")
		}
		return f.event, json.RawMessage(f.data), nil
	}
}

func streamError(code calyx.Code, detail string) *calyx.Error {
	return &calyx.Error{
		Source: calyx.SourceClient,
		Code:   code,
		Detail: detail,
		Retry:  calyx.AdviceFor(calyx.SourceClient, code),
	}
}

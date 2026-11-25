// Command coreerrors writes ../testdata/core-errors.json. Run it in the Go module folder:
//
//	go run ./internal/gen/coreerrors
//
// Each case holds a serialized google.rpc.Status, the value of the grpc-status-details-bin
// header. The core client tests read the file.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

const outputPath = "../testdata/core-errors.json"

type file struct {
	Description string     `json:"description"`
	Cases       []testCase `json:"cases"`
}

type testCase struct {
	Name         string   `json:"name"`
	GRPCCode     int      `json:"grpcCode"`
	StatusBase64 string   `json:"statusBase64"`
	Expected     expected `json:"expected"`
}

type expected struct {
	Source            string            `json:"source"`
	Code              string            `json:"code"`
	Status            int               `json:"status"`
	Retry             string            `json:"retry"`
	RetryAfterSeconds int               `json:"retryAfterSeconds"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// build serializes the status and fills the expected values. The retry advice comes from
// calyx.AdviceFor, so the file always agrees with the SDK table.
func build(name string, st *status.Status, code string, metadata map[string]string, retryAfterSeconds int) (testCase, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(st.Proto())
	if err != nil {
		return testCase{}, fmt.Errorf("marshal the status of %q: %w", name, err)
	}
	return testCase{
		Name:         name,
		GRPCCode:     int(st.Code()),
		StatusBase64: base64.StdEncoding.EncodeToString(raw),
		Expected: expected{
			Source:            string(calyx.SourceCore),
			Code:              code,
			Status:            int(st.Code()),
			Retry:             string(calyx.AdviceFor(calyx.SourceCore, calyx.Code(code))),
			RetryAfterSeconds: retryAfterSeconds,
			Metadata:          metadata,
		},
	}, nil
}

func withInfo(code codes.Code, msg string, details ...protoadapt.MessageV1) (*status.Status, error) {
	st, err := status.New(code, msg).WithDetails(details...)
	if err != nil {
		return nil, fmt.Errorf("add the detail to %q: %w", msg, err)
	}
	return st, nil
}

func run() error {
	at := map[string]string{"at": "2026-11-20T10:00:00Z"}

	calyxStatus, err := withInfo(codes.AlreadyExists, "The outcome is unknown.", &errdetails.ErrorInfo{
		Reason:   "ERROR_REASON_SEND_OUTCOME_UNKNOWN",
		Domain:   "calyx.core.v1",
		Metadata: at,
	})
	if err != nil {
		return err
	}
	otherStatus, err := withInfo(codes.Unavailable, "Quota error.", &errdetails.ErrorInfo{
		Reason: "RATE_LIMIT_EXCEEDED",
		Domain: "example.com",
	})
	if err != nil {
		return err
	}
	emptyStatus, err := withInfo(codes.NotFound, "No such instance.", &errdetails.ErrorInfo{
		Domain: "calyx.core.v1",
	})
	if err != nil {
		return err
	}
	queueFullStatus, err := withInfo(codes.ResourceExhausted, "The queue of the instance is full.",
		&errdetails.ErrorInfo{Reason: "ERROR_REASON_INSTANCE_QUEUE_FULL", Domain: "calyx.core.v1"},
		&errdetails.RetryInfo{RetryDelay: durationpb.New(12 * time.Second)},
	)
	if err != nil {
		return err
	}
	fractionStatus, err := withInfo(codes.ResourceExhausted, "The queue of the instance is full.",
		&errdetails.ErrorInfo{Reason: "ERROR_REASON_INSTANCE_QUEUE_FULL", Domain: "calyx.core.v1"},
		&errdetails.RetryInfo{RetryDelay: durationpb.New(1200 * time.Millisecond)},
	)
	if err != nil {
		return err
	}
	plainStatus := status.New(codes.DeadlineExceeded, "The call ran out of time.")

	inputs := []struct {
		name              string
		st                *status.Status
		code              string
		metadata          map[string]string
		retryAfterSeconds int
	}{
		{"calyx reason with metadata", calyxStatus, "ERROR_REASON_SEND_OUTCOME_UNKNOWN", at, 0},
		{"other domain", otherStatus, "UNAVAILABLE", nil, 0},
		{"empty reason", emptyStatus, "NOT_FOUND", nil, 0},
		{"no details", plainStatus, "DEADLINE_EXCEEDED", nil, 0},
		{"instance queue full with retry info", queueFullStatus, "ERROR_REASON_INSTANCE_QUEUE_FULL", nil, 12},
		{"retry info with a fraction of a second", fractionStatus, "ERROR_REASON_INSTANCE_QUEUE_FULL", nil, 2},
	}

	out := file{
		Description: "Made by: go run ./internal/gen/coreerrors (in packages/client-sdk/go)",
		Cases:       make([]testCase, 0, len(inputs)),
	}
	for _, in := range inputs {
		c, err := build(in.name, in.st, in.code, in.metadata, in.retryAfterSeconds)
		if err != nil {
			return err
		}
		out.Cases = append(out.Cases, c)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the file: %w", err)
	}
	return os.WriteFile(outputPath, append(data, '\n'), 0o644) //nolint:gosec // A test data file, not a secret.
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coreerrors:", err)
		os.Exit(1)
	}
}

package core

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/client-sdk/go/internal/key"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// ListInstances lists the instances of the core.
func (c *Client) ListInstances(ctx context.Context, req *corev1.ListInstancesRequest) (*corev1.ListInstancesResponse, error) {
	return unary(ctx, c, otherLimit, "", req, c.instances.ListInstances)
}

// GetInstanceStatus gives the state of one instance.
func (c *Client) GetInstanceStatus(ctx context.Context, req *corev1.GetInstanceStatusRequest) (*corev1.GetInstanceStatusResponse, error) {
	return unary(ctx, c, otherLimit, "", req, c.instances.GetInstanceStatus)
}

// RemoveInstance removes one instance.
func (c *Client) RemoveInstance(ctx context.Context, req *corev1.RemoveInstanceRequest) (*corev1.RemoveInstanceResponse, error) {
	return unary(ctx, c, otherLimit, "", req, c.instances.RemoveInstance)
}

// UpdateInstanceConfig changes the configuration of one instance.
func (c *Client) UpdateInstanceConfig(ctx context.Context, req *corev1.UpdateInstanceConfigRequest) (*corev1.UpdateInstanceConfigResponse, error) {
	return unary(ctx, c, otherLimit, "", req, c.instances.UpdateInstanceConfig)
}

// GetBulkJob gives the state of one bulk job.
func (c *Client) GetBulkJob(ctx context.Context, req *corev1.GetBulkJobRequest) (*corev1.BulkJob, error) {
	return unary(ctx, c, otherLimit, "", req, c.messages.GetBulkJob)
}

// GetBulkJobs gives the state of 1 to 50 bulk jobs. The core leaves out a job it does not know,
// and the order of the answer is not defined.
func (c *Client) GetBulkJobs(ctx context.Context, req *corev1.GetBulkJobsRequest) (*corev1.GetBulkJobsResponse, error) {
	return unary(ctx, c, otherLimit, "", req, c.messages.GetBulkJobs)
}

// CancelBulkJob cancels one bulk job.
func (c *Client) CancelBulkJob(ctx context.Context, req *corev1.CancelBulkJobRequest) (*corev1.BulkJob, error) {
	return unary(ctx, c, otherLimit, "", req, c.messages.CancelBulkJob)
}

// SendPlainText sends one text message. The result and every error carry the idempotency key.
// The SDK never sends the request again by itself.
// An empty key counts as no key.
func (c *Client) SendPlainText(ctx context.Context, req *corev1.SendPlainTextRequest) (calyx.Sent[*corev1.SendPlainTextResponse], error) {
	return send(ctx, c, sendLimit, req, func(r *corev1.SendPlainTextRequest, k string) { r.IdempotencyKey = &k }, c.messages.SendPlainText)
}

// SendBulkSameMessage starts a bulk job that sends the same message to many recipients.
// The result and every error carry the idempotency key. An empty key counts as no key.
func (c *Client) SendBulkSameMessage(ctx context.Context, req *corev1.SendBulkSameMessageRequest) (calyx.Sent[*corev1.BulkJob], error) {
	return send(ctx, c, otherLimit, req, func(r *corev1.SendBulkSameMessageRequest, k string) { r.IdempotencyKey = &k }, c.messages.SendBulkSameMessage)
}

// SendBulkCustomMessages starts a bulk job that sends a different message to each recipient.
// The result and every error carry the idempotency key. An empty key counts as no key.
func (c *Client) SendBulkCustomMessages(ctx context.Context, req *corev1.SendBulkCustomMessagesRequest) (calyx.Sent[*corev1.BulkJob], error) {
	return send(ctx, c, otherLimit, req, func(r *corev1.SendBulkCustomMessagesRequest, k string) { r.IdempotencyKey = &k }, c.messages.SendBulkCustomMessages)
}

// keyed is a request of a send call: a protobuf message with an optional idempotency key.
type keyed interface {
	proto.Message
	GetIdempotencyKey() string
}

// send is the helper of the three send calls. It keeps the key of the program. Without one,
// it calls setKey on a copy, so the request of the program never changes.
func send[Req keyed, Res any](ctx context.Context, c *Client, limit time.Duration, req Req,
	setKey func(Req, string), rpc func(context.Context, Req, ...grpc.CallOption) (Res, error),
) (calyx.Sent[Res], error) {
	// A nil request is an empty request. Clone gives nil for it, and setKey would panic.
	if !req.ProtoReflect().IsValid() {
		req = req.ProtoReflect().Type().New().Interface().(Req)
	}
	idemKey := req.GetIdempotencyKey()
	if idemKey == "" {
		idemKey = key.New()
		req = proto.Clone(req).(Req)
		setKey(req, idemKey)
	}
	res, err := unary(ctx, c, limit, idemKey, req, rpc)
	if err != nil {
		return calyx.Sent[Res]{IdempotencyKey: idemKey}, err
	}
	return calyx.Sent[Res]{Data: res, IdempotencyKey: idemKey}, nil
}

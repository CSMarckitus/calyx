package core

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// connectParams caps the wait between connections at 5 s (the default is 2 minutes), so the core
// link comes back soon after a core restart.
var connectParams = func() grpc.ConnectParams {
	b := backoff.DefaultConfig
	b.MaxDelay = 5 * time.Second
	return grpc.ConnectParams{Backoff: b, MinConnectTimeout: 20 * time.Second}
}()

// Dial builds the one connection to the core. It does no I/O: the first call reports a core that
// is down. The core sits on a private network, so the transport is not encrypted.
func Dial(address, token string) (*grpc.ClientConn, error) {
	unary, stream := TokenInterceptors(token)
	return grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(connectParams),
		grpc.WithChainUnaryInterceptor(unary),
		grpc.WithChainStreamInterceptor(stream))
}

// TokenInterceptors return interceptors that add "authorization: Bearer <token>" to every call.
// Metadata already on the context stays.
func TokenInterceptors(token string) (grpc.UnaryClientInterceptor, grpc.StreamClientInterceptor) {
	header := "Bearer " + token
	unary := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
	) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, "authorization", header), method, req, reply, cc, opts...)
	}
	stream := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, "authorization", header), desc, cc, method, opts...)
	}
	return unary, stream
}

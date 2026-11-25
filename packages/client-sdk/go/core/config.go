package core

import (
	"crypto/tls"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config is the configuration of New.
type Config struct {
	// Address is the gRPC address of the core, for example "core.internal:50051". It is
	// required.
	Address string
	// Token is the core token. Every call sends it as "authorization: Bearer <Token>".
	Token string
	// Security is required: use Insecure() or TLS(cfg). The zero value gives invalid_config.
	Security Security
}

// Security is the transport security of the connection to the core. Its zero value is not
// valid, so a Config must choose Insecure or TLS.
type Security struct {
	creds credentials.TransportCredentials
}

// Insecure gives a connection without TLS. The core token then travels as plain text, so use
// it only on a private network that you trust.
func Insecure() Security {
	return Security{creds: insecure.NewCredentials()}
}

// TLS gives a connection with TLS. A nil cfg uses the root certificates of the system. The
// client uses a copy of cfg.
func TLS(cfg *tls.Config) Security {
	return Security{creds: credentials.NewTLS(cfg)}
}

// Filter selects the events of Subscribe.
type Filter struct {
	// InstanceNames selects the instances. Empty means all instances.
	InstanceNames []string
	// EventTypes selects the event types. Empty means all event types.
	EventTypes []string
}

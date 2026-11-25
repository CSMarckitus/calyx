package backend

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// Config is the configuration of a Client.
type Config struct {
	// BaseURL is the address of the backend, for example "https://calyx.example.com". It is
	// required.
	BaseURL string
	// APIKey is sent as "Authorization: Bearer <APIKey>". It is optional.
	APIKey string
	// HTTPClient is the client that sends the requests. It is optional. New uses a copy of it
	// with Timeout set to 0: use ctx to limit a call.
	HTTPClient *http.Client
}

// Client calls the HTTP API of the backend. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// SendOptions changes a send call (SendMessage and CreateBulkJob).
type SendOptions struct {
	// IdempotencyKey is the key of the send. When it is empty, the SDK makes a new key.
	IdempotencyKey string
}

// New makes a Client. A BaseURL without the scheme http or https, or without a host, gives a
// *calyx.Error with the code invalid_config.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, clientError(calyx.CodeInvalidConfig, "The base URL is empty.", "", nil)
	}
	// "localhost:8080" parses with the scheme "localhost", so the scheme is checked by name.
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, clientError(calyx.CodeInvalidConfig, "The base URL must start with http:// or https:// and have a host.", "", nil)
	}
	// A Timeout would stop FollowPairing and cut the long send calls. The copy keeps the
	// transport, so the connection pool and the proxy settings stay.
	hc := http.Client{}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}
	hc.Timeout = 0
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		http:    &hc,
	}, nil
}

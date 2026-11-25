package main

import (
	"io"
	"net"
	"time"
)

const (
	// proxyOpen is how long a connection of the proxy carries bytes. Then it stays open and
	// carries nothing, as when the host of the core disappears.
	proxyOpen = 2 * time.Second
	// proxyEnd is when the proxy closes a connection. Without it, a client with no keepalive
	// would wait with no end.
	proxyEnd = 8 * time.Second
)

// serveProxy accepts connections on lis and joins each one with a new connection to addr.
func serveProxy(lis net.Listener, addr string) error {
	for {
		client, err := lis.Accept()
		if err != nil {
			return err
		}
		go proxy(client, addr)
	}
}

func proxy(client net.Conn, addr string) {
	defer func() { _ = client.Close() }()
	core, err := net.Dial("tcp", addr)
	if err != nil {
		return
	}
	defer func() { _ = core.Close() }()
	start := time.Now()
	ended := make(chan struct{}, 2)
	for _, pair := range [][2]net.Conn{{core, client}, {client, core}} {
		go func() {
			carry(pair[0], pair[1], start)
			ended <- struct{}{}
		}()
	}
	select {
	case <-ended:
	case <-time.After(proxyEnd):
	}
}

// carry copies from one side to the other for proxyOpen. After that it drops the bytes.
func carry(to io.Writer, from io.Reader, start time.Time) {
	buf := make([]byte, 64<<10)
	for {
		n, err := from.Read(buf)
		if err != nil {
			return
		}
		if time.Since(start) > proxyOpen {
			continue
		}
		if _, err := to.Write(buf[:n]); err != nil {
			return
		}
	}
}

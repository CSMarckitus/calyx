// Command fakecore is a small gRPC core for the tests of the PHP core client. It answers every
// call from memory. It never talks to WhatsApp and it binds to the loopback address by default.
package main

import (
	"flag"
	"log"
	"net"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:50099", "address to listen on")
	proxyAddr := flag.String("proxy-addr", "127.0.0.1:50098", "address of the proxy that stops to carry bytes")
	flag.Parse()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	proxyLis, err := net.Listen("tcp", *proxyAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fake core listening on %s, with the proxy on %s", lis.Addr(), proxyLis.Addr())
	go func() { log.Fatal(serveProxy(proxyLis, *addr)) }()
	log.Fatal(newServer().Serve(lis))
}

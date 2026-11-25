// Package core is the Go client for the gRPC API of the Calyx core.
//
// The core has no users and no tenants. Use this client only on the private network of the
// core. Every error of a call is a *calyx.Error. The client never sends a request again by
// itself.
package core

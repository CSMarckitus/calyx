// Package tenancy holds the principal, its scope, the role rules, organizations, clients and the
// reservation of instances (the one place where the instance quota is checked).
package tenancy

import (
	"github.com/google/uuid"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleClient Role = "client"
)

// Principal is the authenticated user. Only package auth creates it.
type Principal struct {
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	Role           Role
	ClientID       *uuid.UUID // nil for owners
	APIKeyID       *uuid.UUID // nil for a login cookie
}

// ScopeFilter holds the two values of the tenant predicate of every scoped query.
type ScopeFilter struct {
	OrganizationID uuid.UUID
	ClientID       *uuid.UUID
}

// Scope returns the filter of p. The client id is a copy.
func Scope(p Principal) ScopeFilter {
	f := ScopeFilter{OrganizationID: p.OrganizationID}
	if p.ClientID != nil {
		c := *p.ClientID
		f.ClientID = &c
	}
	return f
}

// Covers tells if the filter sees a row of the organization and the client. An owner sees the whole
// organization. A client user sees only its client. Nobody sees another organization.
func (f ScopeFilter) Covers(org, client uuid.UUID) bool {
	return f.OrganizationID == org && (f.ClientID == nil || *f.ClientID == client)
}

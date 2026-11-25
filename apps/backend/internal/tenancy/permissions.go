package tenancy

import (
	"fmt"
	"slices"

	"calyx/backend/internal/apierr"
)

type Action string

const (
	ManageOrganization Action = "manage_organization" // read, rename, acknowledge
	ManageClients      Action = "manage_clients"
	UseInstances       Action = "use_instances"      // list, read, change, pair, remove, send, bulk
	CreateInstance     Action = "create_instance"    // role check only; quota is separate
	ManageOwnAccount   Action = "manage_own_account" // own password and API keys
)

// permissions is the role table: the actions of each role.
// The owner has every action, so its list is also the list of known actions.
var permissions = map[Role][]Action{
	RoleOwner:  {ManageOrganization, ManageClients, UseInstances, CreateInstance, ManageOwnAccount},
	RoleClient: {UseInstances, CreateInstance, ManageOwnAccount},
}

// Allow returns nil, or 403 owner_only. A principal that cannot exist, or an unknown action,
// returns 500 internal: it can only come from a bug.
func Allow(p Principal, a Action) error {
	switch {
	case p.Role == RoleOwner && p.ClientID == nil:
	case p.Role == RoleClient && p.ClientID != nil:
	default:
		return apierr.Internal(fmt.Errorf("invalid principal: role %q", p.Role))
	}
	if !slices.Contains(permissions[RoleOwner], a) {
		return apierr.Internal(fmt.Errorf("unknown action %q", a))
	}
	if !slices.Contains(permissions[p.Role], a) {
		return apierr.Forbidden(apierr.CodeOwnerOnly, "Only the owner of the organization can do this.")
	}
	return nil
}

// RolesFor lists the roles that can do the action, owner first. The API documentation uses it,
// so the document and Allow read the same table. An unknown action gives an empty list.
func RolesFor(a Action) []Role {
	var roles []Role
	for _, r := range []Role{RoleOwner, RoleClient} {
		if slices.Contains(permissions[r], a) {
			roles = append(roles, r)
		}
	}
	return roles
}

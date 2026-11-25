package tenancy_test

import (
	"bufio"
	"fmt"
	"slices"
	"strings"
	"testing"

	"calyx/backend/internal/tenancy"
)

func TestAllowFollowsTheRoleTable(t *testing.T) {
	owner, user := principals()
	all := []tenancy.Action{tenancy.ManageOrganization, tenancy.ManageClients, tenancy.UseInstances, tenancy.CreateInstance, tenancy.ManageOwnAccount}
	for _, a := range all {
		if err := tenancy.Allow(owner, a); err != nil {
			t.Errorf("owner %s: %v", a, err)
		}
	}
	for _, a := range []tenancy.Action{tenancy.UseInstances, tenancy.CreateInstance, tenancy.ManageOwnAccount} {
		if err := tenancy.Allow(user, a); err != nil {
			t.Errorf("user %s: %v", a, err)
		}
	}
	for _, a := range []tenancy.Action{tenancy.ManageOrganization, tenancy.ManageClients} {
		requireCode(t, tenancy.Allow(user, a), 403, "owner_only")
	}
}

func TestRolesForFollowsAllow(t *testing.T) {
	owner, user := principals()
	all := []tenancy.Action{tenancy.ManageOrganization, tenancy.ManageClients, tenancy.UseInstances, tenancy.CreateInstance, tenancy.ManageOwnAccount}
	for _, a := range all {
		roles := tenancy.RolesFor(a)
		for _, p := range []tenancy.Principal{owner, user} {
			if got, want := slices.Contains(roles, p.Role), tenancy.Allow(p, a) == nil; got != want {
				t.Errorf("%s, role %s: in RolesFor = %v, allowed = %v", a, p.Role, got, want)
			}
		}
	}
}

func TestRolesForUnknownAction(t *testing.T) {
	if roles := tenancy.RolesFor("delete_everything"); len(roles) != 0 {
		t.Errorf("roles = %v, want none", roles)
	}
}

func TestAllowDeniesAnUnknownAction(t *testing.T) {
	owner, user := principals()
	requireCode(t, tenancy.Allow(owner, "delete_everything"), 500, "internal")
	requireCode(t, tenancy.Allow(user, "delete_everything"), 500, "internal")
}

func TestAllowDeniesAnInvalidPrincipal(t *testing.T) {
	c := clientID
	cases := map[string]tenancy.Principal{
		"admin":                 {OrganizationID: orgID, Role: "admin"},
		"empty":                 {OrganizationID: orgID, Role: ""},
		"owner with client":     {OrganizationID: orgID, Role: tenancy.RoleOwner, ClientID: &c},
		"client without client": {OrganizationID: orgID, Role: tenancy.RoleClient},
	}
	all := []tenancy.Action{tenancy.ManageOrganization, tenancy.ManageClients, tenancy.UseInstances, tenancy.CreateInstance, tenancy.ManageOwnAccount}
	for name, p := range cases {
		for _, a := range all {
			t.Run(fmt.Sprintf("%s/%s", name, a), func(t *testing.T) {
				requireCode(t, tenancy.Allow(p, a), 500, "internal")
			})
		}
	}
}

// rolesTable is the role table of Allow, in the form that people read it.
const rolesTable = "| Action | Owner | Client user |\n" +
	"|---|---|---|\n" +
	"| Organization: read, rename, accept the WhatsApp Web text | yes | 403 `owner_only` |\n" +
	"| Clients: list, create, change, delete; invite their user | yes | 403 `owner_only` |\n" +
	"| Instances and bulk jobs: list, read, change, pair, remove, send | all of the organization | only of their client |\n" +
	"| Create an instance | for any client of the organization | for their client, if `can_create_instances` |\n" +
	"| Own API keys and password | yes | yes |\n"

// tableActions maps the first cell of each row of rolesTable to its action.
var tableActions = map[string]tenancy.Action{
	"Organization: read, rename, accept the WhatsApp Web text":        tenancy.ManageOrganization,
	"Clients: list, create, change, delete; invite their user":        tenancy.ManageClients,
	"Instances and bulk jobs: list, read, change, pair, remove, send": tenancy.UseInstances,
	"Create an instance":        tenancy.CreateInstance,
	"Own API keys and password": tenancy.ManageOwnAccount,
}

// A cell "403 `owner_only`" means the role may not do the action; any other text means it may.
func TestAllowMatchesTheRoleTable(t *testing.T) {
	owner, user := principals()
	seen := map[tenancy.Action]bool{}
	inTable := false
	sc := bufio.NewScanner(strings.NewReader(rolesTable))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "| Action | Owner | Client user |") {
			inTable = true
			continue
		}
		if !inTable || strings.HasPrefix(line, "|---") {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 {
			t.Fatalf("row %q has %d cells", line, len(cells))
		}
		a, ok := tableActions[strings.TrimSpace(cells[0])]
		if !ok {
			t.Fatalf("the row %q has no action in this test", cells[0])
		}
		seen[a] = true
		for i, p := range []tenancy.Principal{owner, user} {
			denied := strings.TrimSpace(cells[i+1]) == "403 `owner_only`"
			err := tenancy.Allow(p, a)
			if denied {
				requireCode(t, err, 403, "owner_only")
			} else if err != nil {
				t.Errorf("%s %s: %v", p.Role, a, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(tableActions) {
		t.Fatalf("the table has %d of %d actions", len(seen), len(tableActions))
	}
}

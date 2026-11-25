package tenancy_test

import (
	"testing"

	"github.com/google/uuid"

	"calyx/backend/internal/tenancy"
)

var (
	orgID    = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	clientID = uuid.MustParse("00000000-0000-0000-0000-00000000000c")
)

func principals() (owner, user tenancy.Principal) {
	c := clientID
	owner = tenancy.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: tenancy.RoleOwner}
	user = tenancy.Principal{UserID: uuid.New(), OrganizationID: orgID, Role: tenancy.RoleClient, ClientID: &c}
	return
}

func TestScopeOfAnOwnerHasNoClient(t *testing.T) {
	owner, _ := principals()
	f := tenancy.Scope(owner)
	if f.OrganizationID != orgID || f.ClientID != nil {
		t.Fatalf("unexpected scope %+v", f)
	}
}

func TestScopeOfAClientUserHasItsClient(t *testing.T) {
	_, user := principals()
	f := tenancy.Scope(user)
	if f.OrganizationID != orgID || f.ClientID == nil || *f.ClientID != clientID {
		t.Fatalf("unexpected scope %+v", f)
	}
}

func TestScopeCopiesTheClientID(t *testing.T) {
	_, user := principals()
	f := tenancy.Scope(user)
	*f.ClientID = uuid.New()
	if *user.ClientID != clientID {
		t.Fatal("Scope shares the client id with the principal")
	}
}

func TestScopeCovers(t *testing.T) {
	owner, user := principals()
	otherClient, otherOrg := uuid.New(), uuid.New()
	tests := []struct {
		name   string
		p      tenancy.Principal
		org    uuid.UUID
		client uuid.UUID
		want   bool
	}{
		{"owner, any client of its organization", owner, orgID, otherClient, true},
		{"owner, another organization", owner, otherOrg, clientID, false},
		{"client user, its client", user, orgID, clientID, true},
		{"client user, another client", user, orgID, otherClient, false},
		{"client user, another organization", user, otherOrg, clientID, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tenancy.Scope(tt.p).Covers(tt.org, tt.client); got != tt.want {
				t.Fatalf("Covers = %v, want %v", got, tt.want)
			}
		})
	}
}

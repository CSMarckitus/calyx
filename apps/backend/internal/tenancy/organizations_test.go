package tenancy_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"calyx/backend/internal/tenancy"
)

func ptr[T any](v T) *T { return &v }

func TestCreateOrganizationCreatesItsDefaultClient(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, err := s.CreateOrganization(context.Background(), tenancy.NewOrganization{Name: "Gym Group", MaxInstances: 10, WhatsAppWebAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if org.Name != "Gym Group" || org.MaxInstances != 10 || !org.WhatsappWebAllowed ||
		org.WhatsappWebAcknowledgedAt != nil || org.WhatsappWebAcknowledgedBy != nil {
		t.Fatalf("bad organization %+v", org)
	}
	if !c.IsDefault || c.Name != "Default" || c.CanCreateInstances || c.MaxInstances != 0 || c.OrganizationID != org.ID {
		t.Fatalf("bad client %+v", c)
	}
	if n := countRows(t, pool, "SELECT count(*) FROM clients WHERE organization_id=$1 AND is_default", org.ID); n != 1 {
		t.Fatalf("default clients = %d", n)
	}
}

func TestCreateOrganizationTrimsTheName(t *testing.T) {
	s, _, _ := newService(t)
	org, _, err := s.CreateOrganization(context.Background(), tenancy.NewOrganization{Name: "  Gym  ", MaxInstances: 1})
	if err != nil || org.Name != "Gym" {
		t.Fatalf("%v %q", err, org.Name)
	}
}

func TestCreateOrganizationRejectsInvalidInput(t *testing.T) {
	s, pool, _ := newService(t)
	bad := []tenancy.NewOrganization{
		{Name: "", MaxInstances: 1},
		{Name: "   ", MaxInstances: 1},
		{Name: strings.Repeat("é", 101), MaxInstances: 1},
		{Name: "A", MaxInstances: -1},
		{Name: "A", MaxInstances: 10001},
	}
	for _, in := range bad {
		_, _, err := s.CreateOrganization(context.Background(), in)
		requireCode(t, err, 400, "invalid_request")
	}
	if n := countRows(t, pool, "SELECT count(*) FROM organizations"); n != 0 {
		t.Fatalf("organizations = %d", n)
	}
	for i, in := range []tenancy.NewOrganization{
		{Name: strings.Repeat("é", 100), MaxInstances: 0},
		{Name: "B", MaxInstances: 10000},
	} {
		if _, _, err := s.CreateOrganization(context.Background(), in); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestUpdateOrganizationChangesOnlyTheGivenFields(t *testing.T) {
	s, _, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	got, err := s.UpdateOrganization(context.Background(), org.ID, tenancy.OrganizationChange{MaxInstances: ptr(3)})
	if err != nil || got.MaxInstances != 3 || got.Name != org.Name || !got.WhatsappWebAllowed {
		t.Fatalf("%v %+v", err, got)
	}
	got, err = s.UpdateOrganization(context.Background(), org.ID, tenancy.OrganizationChange{WhatsAppWebAllowed: ptr(false)})
	if err != nil || got.WhatsappWebAllowed || got.MaxInstances != 3 {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestUpdateOrganizationWithNoChangeReturnsTheRow(t *testing.T) {
	s, _, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	got, err := s.UpdateOrganization(context.Background(), org.ID, tenancy.OrganizationChange{})
	if err != nil || got.ID != org.ID || !got.UpdatedAt.Equal(org.UpdatedAt) {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestUpdateOfAnUnknownOrganizationIsNotFound(t *testing.T) {
	s, _, _ := newService(t)
	_, err := s.UpdateOrganization(context.Background(), uuid.New(), tenancy.OrganizationChange{MaxInstances: ptr(1)})
	requireCode(t, err, 404, "not_found")
	_, err = s.UpdateOrganization(context.Background(), uuid.New(), tenancy.OrganizationChange{})
	requireCode(t, err, 404, "not_found")
}

func TestUpdateOrganizationRejectsInvalidInput(t *testing.T) {
	s, _, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	_, err := s.UpdateOrganization(context.Background(), org.ID, tenancy.OrganizationChange{Name: ptr("  ")})
	requireCode(t, err, 400, "invalid_request")
	_, err = s.UpdateOrganization(context.Background(), org.ID, tenancy.OrganizationChange{MaxInstances: ptr(10001)})
	requireCode(t, err, 400, "invalid_request")
}

func TestBlockingWhatsAppWebKeepsTheAcknowledgement(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	if _, err := s.AcknowledgeWhatsAppWeb(context.Background(), p, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.UpdateOrganization(context.Background(), org.ID, tenancy.OrganizationChange{WhatsAppWebAllowed: ptr(false)})
	if err != nil || got.WhatsappWebAcknowledgedAt == nil {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestAcknowledgeStoresTimeAndUser(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	got, err := s.AcknowledgeWhatsAppWeb(context.Background(), p, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.WhatsappWebAcknowledgedAt == nil || !got.WhatsappWebAcknowledgedAt.Equal(start) ||
		got.WhatsappWebAcknowledgedBy == nil || *got.WhatsappWebAcknowledgedBy != p.UserID {
		t.Fatalf("%+v", got)
	}
}

func TestASecondAcknowledgementKeepsTheFirst(t *testing.T) {
	s, pool, clk := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	if _, err := s.AcknowledgeWhatsAppWeb(context.Background(), p, true); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	got, err := s.AcknowledgeWhatsAppWeb(context.Background(), p, true)
	if err != nil || !got.WhatsappWebAcknowledgedAt.Equal(start) {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestAcknowledgeWorksWhenWhatsAppWebIsBlocked(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, false)
	got, err := s.AcknowledgeWhatsAppWeb(context.Background(), ownerOf(t, pool, org), true)
	if err != nil || got.WhatsappWebAcknowledgedAt == nil {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestAClientUserCannotAcknowledgeOrReadOrRename(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	u := userOf(t, pool, c)
	ctx := context.Background()
	_, err := s.AcknowledgeWhatsAppWeb(ctx, u, true)
	requireCode(t, err, 403, "owner_only")
	_, err = s.GetOrganization(ctx, u)
	requireCode(t, err, 403, "owner_only")
	_, err = s.RenameOrganization(ctx, u, "Other")
	requireCode(t, err, 403, "owner_only")
	var name string
	var ack *time.Time
	if err := pool.QueryRow(ctx, "SELECT name, whatsapp_web_acknowledged_at FROM organizations WHERE id=$1", org.ID).Scan(&name, &ack); err != nil {
		t.Fatal(err)
	}
	if name != org.Name || ack != nil {
		t.Fatalf("organization changed: %q %v", name, ack)
	}
}

func TestGetOrganizationCountsEveryInstance(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	for _, st := range []string{"creating", "active", "missing"} {
		insertInstance(t, pool, org.ID, c.ID, st)
	}
	v, err := s.GetOrganization(context.Background(), ownerOf(t, pool, org))
	if err != nil || v.InstanceCount != 3 || v.ID != org.ID {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestRenameOrganization(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	v, err := s.RenameOrganization(context.Background(), p, " New name ")
	if err != nil || v.Name != "New name" {
		t.Fatalf("%v %+v", err, v)
	}
	_, err = s.RenameOrganization(context.Background(), p, "")
	requireCode(t, err, 400, "invalid_request")
}

func TestListOrganizationsInCreationOrder(t *testing.T) {
	s, pool, _ := newService(t)
	a, ca := newOrg(t, s, 10, true)
	b, _ := newOrg(t, s, 10, true)
	insertInstance(t, pool, a.ID, ca.ID, "active")
	list, err := s.ListOrganizations(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %d", err, len(list))
	}
	if list[0].ID != a.ID || list[0].InstanceCount != 1 || list[1].ID != b.ID || list[1].InstanceCount != 0 {
		t.Fatalf("%+v", list)
	}
}

func TestNamesRefuseControlCharacters(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()
	for _, name := range []string{"a\nb", "a\x00b", "a b", "a b", "a\tb"} {
		_, _, err := s.CreateOrganization(ctx, tenancy.NewOrganization{Name: name, MaxInstances: 1})
		requireCode(t, err, 400, "invalid_request")
	}
	org, def := newOrg(t, s, 5, true)
	bad := "x\ny"
	_, err := s.UpdateOrganization(ctx, org.ID, tenancy.OrganizationChange{Name: &bad})
	requireCode(t, err, 400, "invalid_request")
	owner := tenancy.Principal{UserID: uuid.New(), OrganizationID: org.ID, Role: tenancy.RoleOwner}
	_, err = s.RenameOrganization(ctx, owner, bad)
	requireCode(t, err, 400, "invalid_request")
	_, err = s.CreateClient(ctx, owner, tenancy.ClientInput{Name: bad})
	requireCode(t, err, 400, "invalid_request")
	c, err := s.CreateClient(ctx, owner, tenancy.ClientInput{Name: "Fine"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateClient(ctx, owner, c.ID, tenancy.ClientChange{Name: &bad})
	requireCode(t, err, 400, "invalid_request")
	_ = def
}

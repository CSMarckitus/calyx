package tenancy_test

import (
	"context"
	"strings"
	"testing"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/tenancy"
)

// Each limit is accepted at its value and refused one step past it, with the field path.

func TestNameLimitOnEveryMethod(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	ctx := context.Background()
	c, err := s.CreateClient(ctx, p, tenancy.ClientInput{Name: "C"})
	if err != nil {
		t.Fatal(err)
	}
	ok := " " + strings.Repeat("é", tenancy.MaxNameLength) + " "
	long := strings.Repeat("é", tenancy.MaxNameLength+1)
	calls := map[string]func(name string) error{
		"CreateOrganization": func(n string) error {
			_, _, err := s.CreateOrganization(ctx, tenancy.NewOrganization{Name: n, MaxInstances: 1})
			return err
		},
		"UpdateOrganization": func(n string) error {
			_, err := s.UpdateOrganization(ctx, org.ID, tenancy.OrganizationChange{Name: &n})
			return err
		},
		"RenameOrganization": func(n string) error {
			_, err := s.RenameOrganization(ctx, p, n)
			return err
		},
		"CreateClient": func(n string) error {
			_, err := s.CreateClient(ctx, p, tenancy.ClientInput{Name: n})
			return err
		},
		"UpdateClient": func(n string) error {
			_, err := s.UpdateClient(ctx, p, c.ID, tenancy.ClientChange{Name: &n})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(ok); err != nil {
				t.Fatalf("limit: %v", err)
			}
			requireFields(t, call(long), "name")
			requireFields(t, call("   "), "name")
			requireFields(t, call("a\nb"), "name")
		})
	}
}

func TestMaxInstancesLimits(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	ctx := context.Background()
	for _, n := range []int{0, tenancy.MaxInstancesLimit} {
		if _, err := s.CreateClient(ctx, p, tenancy.ClientInput{Name: "C", MaxInstances: n}); err != nil {
			t.Fatalf("client %d: %v", n, err)
		}
		if _, _, err := s.CreateOrganization(ctx, tenancy.NewOrganization{Name: "O", MaxInstances: n}); err != nil {
			t.Fatalf("organization %d: %v", n, err)
		}
	}
	for _, n := range []int{-1, tenancy.MaxInstancesLimit + 1} {
		_, err := s.CreateClient(ctx, p, tenancy.ClientInput{Name: "C", MaxInstances: n})
		requireFields(t, err, "max_instances")
		_, _, err = s.CreateOrganization(ctx, tenancy.NewOrganization{Name: "O", MaxInstances: n})
		requireFields(t, err, "max_instances")
		_, err = s.UpdateOrganization(ctx, org.ID, tenancy.OrganizationChange{MaxInstances: &n})
		requireFields(t, err, "max_instances")
	}
}

func TestEveryWrongFieldIsReportedAtOnce(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	_, err := s.CreateClient(context.Background(), p, tenancy.ClientInput{Name: "", MaxInstances: -1})
	requireFields(t, err, "name", "max_instances")
}

func TestNameMessageDoesNotQuoteTheValue(t *testing.T) {
	s, _, _ := newService(t)
	name := strings.Repeat("secret", 20)
	_, _, err := s.CreateOrganization(context.Background(), tenancy.NewOrganization{Name: name, MaxInstances: 1})
	requireFields(t, err, "name")
	if strings.Contains(err.Error(), "secret") || strings.Contains(errorsText(err), "secret") {
		t.Fatalf("the message quotes the value: %v", err)
	}
}

// The acknowledgement needs accept = true. A refused one stores nothing.
func TestAcknowledgeNeedsAccept(t *testing.T) {
	s, pool, _ := newService(t)
	org, _ := newOrg(t, s, 10, true)
	p := ownerOf(t, pool, org)
	ctx := context.Background()
	_, err := s.AcknowledgeWhatsAppWeb(ctx, p, false)
	requireFields(t, err, "accept")
	if ae, _ := apierr.As(err); ae.Detail != "accept must be true." || ae.Errors[0].Message != "must be true." {
		t.Fatalf("error = %+v", ae)
	}
	view, err := s.GetOrganization(ctx, p)
	if err != nil || view.WhatsappWebAcknowledgedAt != nil {
		t.Fatalf("%v %+v", err, view)
	}
	got, err := s.AcknowledgeWhatsAppWeb(ctx, p, true)
	if err != nil || got.WhatsappWebAcknowledgedAt == nil {
		t.Fatalf("%v %+v", err, got)
	}
}

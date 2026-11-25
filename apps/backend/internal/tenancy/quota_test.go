package tenancy_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

func reserve(t *testing.T, pool *pgxpool.Pool, p tenancy.Principal, in tenancy.NewInstance) (gen.Instance, error) {
	t.Helper()
	var row gen.Instance
	err := pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
		var err error
		row, err = tenancy.ReserveInstance(context.Background(), tx, p, in)
		return err
	})
	return row, err
}

func ww(clientID uuid.UUID) tenancy.NewInstance {
	return tenancy.NewInstance{ClientID: clientID, CoreID: "main", DisplayName: "Front desk", Provider: "WHATSAPP_WEB"}
}

func meta(clientID uuid.UUID, phoneNumberID string) tenancy.NewInstance {
	return tenancy.NewInstance{ClientID: clientID, CoreID: "main", DisplayName: "Meta", Provider: "META_CLOUD", PhoneNumberID: &phoneNumberID}
}

// ready returns an organization that allows and has acknowledged WhatsApp Web, and its owner.
func ready(t *testing.T, s *tenancy.Service, pool *pgxpool.Pool, maxInstances int) (gen.Organization, gen.Client, tenancy.Principal) {
	t.Helper()
	org, c := newOrg(t, s, maxInstances, true)
	owner := ownerOf(t, pool, org)
	if _, err := s.AcknowledgeWhatsAppWeb(context.Background(), owner, true); err != nil {
		t.Fatal(err)
	}
	return org, c, owner
}

func newClient(t *testing.T, s *tenancy.Service, owner tenancy.Principal, in tenancy.ClientInput) gen.Client {
	t.Helper()
	c, err := s.CreateClient(context.Background(), owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func total(t *testing.T, pool *pgxpool.Pool) int {
	return countRows(t, pool, "SELECT count(*) FROM instances")
}

func TestReserveInsertsACreatingRow(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, owner := ready(t, s, pool, 10)
	in := ww(c.ID)
	in.DeviceIdentity = ptr("FIREFOX_LINUX")
	in.Locale = ptr("es-ES")
	row, err := reserve(t, pool, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "creating" || !row.Enabled || row.CoreID != "main" || row.CoreName != tenancy.CoreName(row.ID) ||
		len(row.CoreName) != 34 || row.CoreName[:2] != "i_" || row.OrganizationID != org.ID || row.ClientID != c.ID ||
		row.DisplayName != "Front desk" || row.Provider != "WHATSAPP_WEB" || row.SendOnly ||
		row.DeviceIdentity == nil || *row.DeviceIdentity != "FIREFOX_LINUX" || row.Locale == nil || *row.Locale != "es-ES" ||
		row.PhoneNumberID != nil ||
		row.LastStatus != nil || row.AlertKind != nil || row.AlertAttempts != 0 {
		t.Fatalf("%+v", row)
	}
}

func TestReserveStoresTheMetaPhoneNumberID(t *testing.T) {
	s, pool, _ := newService(t)
	_, c, owner := ready(t, s, pool, 10)
	row, err := reserve(t, pool, owner, meta(c.ID, "1111"))
	if err != nil || row.PhoneNumberID == nil || *row.PhoneNumberID != "1111" {
		t.Fatalf("%v %+v", err, row)
	}
}

func TestTheOrganizationLimitIsEnforced(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, owner := ready(t, s, pool, 2)
	for i := 0; i < 2; i++ {
		if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
	if n := countRows(t, pool, "SELECT count(*) FROM instances WHERE organization_id=$1", org.ID); n != 2 {
		t.Fatalf("rows = %d", n)
	}
}

func TestTheClientLimitIsEnforced(t *testing.T) {
	s, pool, _ := newService(t)
	_, def, owner := ready(t, s, pool, 10)
	c := newClient(t, s, owner, tenancy.ClientInput{Name: "C", CanCreateInstances: true, MaxInstances: 2})
	u := userOf(t, pool, c)
	for i := 0; i < 2; i++ {
		if _, err := reserve(t, pool, u, ww(c.ID)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := reserve(t, pool, u, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
	_, err = reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
	if _, err := reserve(t, pool, owner, ww(def.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestClientMaxZeroMeansOnlyTheOrganizationLimit(t *testing.T) {
	s, pool, _ := newService(t)
	_, _, owner := ready(t, s, pool, 3)
	c := newClient(t, s, owner, tenancy.ClientInput{Name: "C", CanCreateInstances: true})
	for i := 0; i < 3; i++ {
		if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
}

func TestEveryStateCounts(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, owner := ready(t, s, pool, 4)
	for _, st := range []string{"creating", "active", "removing", "missing"} {
		insertInstance(t, pool, org.ID, c.ID, st)
	}
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
}

func TestLoweredOrganizationLimitKeepsInstancesAndRefusesNewOnes(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, owner := ready(t, s, pool, 3)
	for i := 0; i < 3; i++ {
		if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if _, err := s.UpdateOrganization(ctx, org.ID, tenancy.OrganizationChange{MaxInstances: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	if total(t, pool) != 3 {
		t.Fatal("instances were removed")
	}
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
	if _, err := s.UpdateOrganization(ctx, org.ID, tenancy.OrganizationChange{MaxInstances: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
		t.Fatal(err)
	}
	if total(t, pool) != 4 {
		t.Fatal("want 4 rows")
	}
}

func TestLoweredClientLimitKeepsInstancesAndRefusesNewOnes(t *testing.T) {
	s, pool, _ := newService(t)
	_, _, owner := ready(t, s, pool, 10)
	c := newClient(t, s, owner, tenancy.ClientInput{Name: "C", CanCreateInstances: true, MaxInstances: 2})
	for i := 0; i < 2; i++ {
		if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if _, err := s.UpdateClient(ctx, owner, c.ID, tenancy.ClientChange{MaxInstances: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	if total(t, pool) != 2 {
		t.Fatal("instances were removed")
	}
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 409, "quota_exceeded")
	if _, err := s.UpdateClient(ctx, owner, c.ID, tenancy.ClientChange{MaxInstances: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestAClientUserNeedsCanCreateInstances(t *testing.T) {
	s, pool, _ := newService(t)
	_, c, _ := ready(t, s, pool, 10)
	u := userOf(t, pool, c)
	_, err := reserve(t, pool, u, ww(c.ID))
	requireCode(t, err, 403, "instance_creation_not_allowed")
	if total(t, pool) != 0 {
		t.Fatal("a row was inserted")
	}
}

func TestAnOwnerDoesNotNeedCanCreateInstances(t *testing.T) {
	s, pool, _ := newService(t)
	_, c, owner := ready(t, s, pool, 10)
	if _, err := reserve(t, pool, owner, ww(c.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestAClientUserMayCreateWhenAllowed(t *testing.T) {
	s, pool, _ := newService(t)
	_, _, owner := ready(t, s, pool, 10)
	c := newClient(t, s, owner, tenancy.ClientInput{Name: "C", CanCreateInstances: true})
	row, err := reserve(t, pool, userOf(t, pool, c), ww(c.ID))
	if err != nil || row.ClientID != c.ID {
		t.Fatalf("%v %+v", err, row)
	}
}

func TestAClientUserCannotUseAnotherClient(t *testing.T) {
	s, pool, _ := newService(t)
	_, _, owner := ready(t, s, pool, 10)
	c1 := newClient(t, s, owner, tenancy.ClientInput{Name: "C1", CanCreateInstances: true})
	c2 := newClient(t, s, owner, tenancy.ClientInput{Name: "C2", CanCreateInstances: true})
	_, err := reserve(t, pool, userOf(t, pool, c1), ww(c2.ID))
	requireCode(t, err, 404, "not_found")
	if total(t, pool) != 0 {
		t.Fatal("a row was inserted")
	}
}

func TestAnOwnerCannotUseAClientOfAnotherOrganization(t *testing.T) {
	s, pool, _ := newService(t)
	_, ca, _ := ready(t, s, pool, 10)
	_, _, ownerB := ready(t, s, pool, 10)
	_, err := reserve(t, pool, ownerB, ww(ca.ID))
	requireCode(t, err, 404, "not_found")
	if total(t, pool) != 0 {
		t.Fatal("a row was inserted")
	}
}

func TestWhatsAppWebNotAllowed(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, false)
	owner := ownerOf(t, pool, org)
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 403, "whatsapp_web_not_allowed")
	if _, err := reserve(t, pool, owner, meta(c.ID, "5555")); err != nil {
		t.Fatal(err)
	}
}

func TestWhatsAppWebNotAcknowledged(t *testing.T) {
	s, pool, _ := newService(t)
	org, c := newOrg(t, s, 10, true)
	owner := ownerOf(t, pool, org)
	_, err := reserve(t, pool, owner, ww(c.ID))
	requireCode(t, err, 403, "whatsapp_web_not_acknowledged")
	if _, err := reserve(t, pool, owner, meta(c.ID, "6666")); err != nil {
		t.Fatal(err)
	}
}

func TestPhoneNumberIDInUse(t *testing.T) {
	s, pool, _ := newService(t)
	_, ca, ownerA := ready(t, s, pool, 10)
	_, cb, ownerB := ready(t, s, pool, 10)
	if _, err := reserve(t, pool, ownerA, meta(ca.ID, "2222")); err != nil {
		t.Fatal(err)
	}
	_, err := reserve(t, pool, ownerB, meta(cb.ID, "2222"))
	requireCode(t, err, 409, "phone_number_id_in_use")
	for i := 0; i < 2; i++ {
		if _, err := reserve(t, pool, ownerB, ww(cb.ID)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidProviderAndMissingPhoneNumberID(t *testing.T) {
	s, pool, _ := newService(t)
	_, c, owner := ready(t, s, pool, 10)
	bad := ww(c.ID)
	bad.Provider = "TELEGRAM"
	_, err := reserve(t, pool, owner, bad)
	requireCode(t, err, 400, "invalid_request")
	m := ww(c.ID)
	m.Provider = "META_CLOUD"
	_, err = reserve(t, pool, owner, m)
	requireCode(t, err, 400, "invalid_request")
	m.PhoneNumberID = ptr("")
	_, err = reserve(t, pool, owner, m)
	requireCode(t, err, 400, "invalid_request")
}

func TestChecksRunInTheOrderOfTheSpec(t *testing.T) {
	s, pool, _ := newService(t)
	// 1. scope before everything else.
	org1, c1 := newOrg(t, s, 0, false)
	owner1 := ownerOf(t, pool, org1)
	c2 := newClient(t, s, owner1, tenancy.ClientInput{Name: "C2", CanCreateInstances: true})
	_, err := reserve(t, pool, userOf(t, pool, c1), ww(c2.ID))
	requireCode(t, err, 404, "not_found")
	// 2. WhatsApp Web blocked and full.
	_, err = reserve(t, pool, owner1, ww(c1.ID))
	requireCode(t, err, 403, "whatsapp_web_not_allowed")
	// 3. not acknowledged and full.
	org3, c3 := newOrg(t, s, 0, true)
	_, err = reserve(t, pool, ownerOf(t, pool, org3), ww(c3.ID))
	requireCode(t, err, 403, "whatsapp_web_not_acknowledged")
	// 4. client user without can_create_instances, organization full.
	org4, c4, _ := ready(t, s, pool, 1)
	insertInstance(t, pool, org4.ID, c4.ID, "active")
	_, err = reserve(t, pool, userOf(t, pool, c4), ww(c4.ID))
	requireCode(t, err, 403, "instance_creation_not_allowed")
	// 5. organization full and phone number in use: the quota answers first.
	_, c5, owner5 := ready(t, s, pool, 1)
	if _, err := reserve(t, pool, owner5, meta(c5.ID, "3333")); err != nil {
		t.Fatal(err)
	}
	_, err = reserve(t, pool, owner5, meta(c5.ID, "3333"))
	requireCode(t, err, 409, "quota_exceeded")
	if e, _ := apierr.As(err); e == nil || e.Detail != "The organization has reached its limit of 1 instances." {
		t.Fatalf("detail: %v", err)
	}
}

func TestARefusedReservationInsertsNothing(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, owner := ready(t, s, pool, 1)
	insertInstance(t, pool, org.ID, c.ID, "active")
	before := total(t, pool)
	refused := []tenancy.NewInstance{ww(c.ID), meta(c.ID, "7777"), ww(uuid.New())}
	bad := ww(c.ID)
	bad.Provider = "X"
	refused = append(refused, bad)
	for _, in := range refused {
		if _, err := reserve(t, pool, owner, in); err == nil {
			t.Fatal("want an error")
		}
	}
	if total(t, pool) != before {
		t.Fatal("a refused reservation inserted a row")
	}
}

// concurrent runs 20 reservations at once and checks the outcome.
func concurrent(t *testing.T, pool *pgxpool.Pool, p tenancy.Principal, in tenancy.NewInstance) {
	t.Helper()
	const n = 20
	gate := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			_, errs[i] = reserve(t, pool, p, in)
		}()
	}
	close(gate)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		e, isAPI := apierr.As(err)
		if !isAPI || e.Status != 409 || e.Code != "quota_exceeded" {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 2 {
		t.Errorf("successes = %d, want 2", ok)
	}
}

func TestTwentyConcurrentReservationsAgainstALimitOfTwo(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, owner := ready(t, s, pool, 2)
	concurrent(t, pool, owner, ww(c.ID))
	if n := countRows(t, pool, "SELECT count(*) FROM instances WHERE organization_id=$1", org.ID); n != 2 {
		t.Fatalf("rows = %d", n)
	}
}

func TestTwentyConcurrentReservationsAgainstAClientLimitOfTwo(t *testing.T) {
	s, pool, _ := newService(t)
	_, _, owner := ready(t, s, pool, 10)
	c := newClient(t, s, owner, tenancy.ClientInput{Name: "C", CanCreateInstances: true, MaxInstances: 2})
	concurrent(t, pool, userOf(t, pool, c), ww(c.ID))
	if n := countRows(t, pool, "SELECT count(*) FROM instances WHERE client_id=$1", c.ID); n != 2 {
		t.Fatalf("rows = %d", n)
	}
}

func TestConcurrentReservationsOfTheSamePhoneNumberID(t *testing.T) {
	s, pool, _ := newService(t)
	_, ca, ownerA := ready(t, s, pool, 10)
	_, cb, ownerB := ready(t, s, pool, 10)
	ctx := context.Background()
	release := make(chan struct{})
	inserted := make(chan struct{})
	aDone := make(chan error, 1)
	go func() {
		aDone <- pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tenancy.ReserveInstance(ctx, tx, ownerA, meta(ca.ID, "4444")); err != nil {
				return err
			}
			close(inserted)
			<-release
			return nil
		})
	}()
	select {
	case <-inserted:
	case err := <-aDone:
		t.Fatalf("A failed: %v", err)
	}
	bDone := make(chan error, 1)
	go func() {
		_, err := reserve(t, pool, ownerB, meta(cb.ID, "4444"))
		bDone <- err
	}()
	select {
	case err := <-bDone:
		t.Fatalf("B finished before A committed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-aDone; err != nil {
		t.Fatal(err)
	}
	requireCode(t, <-bDone, 409, "phone_number_id_in_use")
	if n := countRows(t, pool, "SELECT count(*) FROM instances WHERE phone_number_id='4444'"); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestAnInvalidPrincipalCannotReserve(t *testing.T) {
	s, pool, _ := newService(t)
	org, c, _ := ready(t, s, pool, 10)
	cid := c.ID
	cases := map[string]tenancy.Principal{
		"unknown role":      {UserID: uuid.New(), OrganizationID: org.ID, Role: "admin"},
		"owner with client": {UserID: uuid.New(), OrganizationID: org.ID, Role: tenancy.RoleOwner, ClientID: &cid},
		"client no client":  {UserID: uuid.New(), OrganizationID: org.ID, Role: tenancy.RoleClient},
	}
	for name, p := range cases {
		_, err := reserve(t, pool, p, ww(c.ID))
		if e, ok := apierr.As(err); !ok || e.Status != 500 || e.Code != "internal" {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if total(t, pool) != 0 {
		t.Fatal("a row was inserted")
	}
}

func TestWhatsAppWebWithAPhoneNumberIDIsInvalid(t *testing.T) {
	s, pool, _ := newService(t)
	_, c, owner := ready(t, s, pool, 10)
	in := ww(c.ID)
	in.PhoneNumberID = ptr("9999")
	_, err := reserve(t, pool, owner, in)
	requireCode(t, err, 400, "invalid_request")
	if total(t, pool) != 0 {
		t.Fatal("a row was inserted")
	}
}

// problemCases run ReserveInstance and CreateInstanceProblem on the same state. want is for a client
// user. owner is what ReserveInstance says for an owner: it skips can_create_instances. An owner
// never gets a problem from CreateInstanceProblem.
var problemCases = []struct {
	name         string
	allowed      bool
	acknowledged bool
	canCreate    bool
	orgMax       int
	clientMax    int
	orgUsed      int // instances of another client
	clientUsed   int
	want, owner  string // "" means no problem
}{
	{"nothing wrong", true, true, true, 10, 0, 0, 0, "", ""},
	{"blocked by the operator", false, true, true, 10, 0, 0, 0, "whatsapp_web_not_allowed", "whatsapp_web_not_allowed"},
	{"blocked and not acknowledged", false, false, false, 0, 0, 0, 0, "whatsapp_web_not_allowed", "whatsapp_web_not_allowed"},
	{"not acknowledged", true, false, true, 10, 0, 0, 0, "whatsapp_web_not_acknowledged", "whatsapp_web_not_acknowledged"},
	{"not acknowledged and no permission", true, false, false, 10, 0, 0, 0, "whatsapp_web_not_acknowledged", "whatsapp_web_not_acknowledged"},
	{"no permission", true, true, false, 10, 0, 0, 0, "instance_creation_not_allowed", ""},
	{"no permission and organization full", true, true, false, 1, 0, 1, 0, "instance_creation_not_allowed", "quota_exceeded"},
	{"organization full", true, true, true, 1, 0, 1, 0, "quota_exceeded", "quota_exceeded"},
	{"client full", true, true, true, 10, 1, 0, 1, "quota_exceeded", "quota_exceeded"},
	{"client below its limit", true, true, true, 10, 2, 0, 1, "", ""},
}

func TestCreationProblemTable(t *testing.T) {
	ctx := context.Background()
	for _, tc := range problemCases {
		for _, asOwner := range []bool{false, true} {
			name := tc.name + "/client user"
			if asOwner {
				name = tc.name + "/owner"
			}
			t.Run(name, func(t *testing.T) {
				s, pool, _ := newService(t)
				org, def := newOrg(t, s, tc.orgMax, tc.allowed)
				owner := ownerOf(t, pool, org)
				if tc.acknowledged {
					if _, err := s.AcknowledgeWhatsAppWeb(ctx, owner, true); err != nil {
						t.Fatal(err)
					}
				}
				c := newClient(t, s, owner, tenancy.ClientInput{Name: "C", CanCreateInstances: tc.canCreate, MaxInstances: tc.clientMax})
				for range tc.orgUsed {
					insertInstance(t, pool, org.ID, def.ID, "active")
				}
				for range tc.clientUsed {
					insertInstance(t, pool, org.ID, c.ID, "active")
				}
				p, wantProblem, wantReserve := userOf(t, pool, c), tc.want, tc.want
				if asOwner {
					p, wantProblem, wantReserve = owner, "", tc.owner
				}
				got, err := s.CreateInstanceProblem(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				if wantProblem == "" && got != nil || wantProblem != "" && (got == nil || *got != wantProblem) {
					t.Fatalf("problem: got %v, want %q", got, wantProblem)
				}
				_, err = reserve(t, pool, p, ww(c.ID))
				if wantReserve == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if e, ok := apierr.As(err); !ok || e.Code != wantReserve {
					t.Fatalf("reserve: got %v, want %q", err, wantReserve)
				}
			})
		}
	}
}

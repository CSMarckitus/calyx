package testutil

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tenant is one organization with its clients and users, written with raw SQL.
type Tenant struct {
	OrganizationID  uuid.UUID
	DefaultClientID uuid.UUID
	ClientA1        uuid.UUID // can_create_instances true, max_instances 0
	ClientA2        uuid.UUID // can_create_instances true, max_instances 0
	OwnerID         uuid.UUID // active owner
	UserA1          uuid.UUID // client user of ClientA1
	UserA2          uuid.UUID // client user of ClientA2
	OwnerEmail      string
}

// TenantOptions sets the organization of SeedTenant.
type TenantOptions struct {
	Name               string // organization name; client names are Name+" default", Name+" A1", Name+" A2"
	MaxInstances       int    // 0 = 10
	WhatsAppWebAllowed bool
	Acknowledged       bool // sets whatsapp_web_acknowledged_at = now(), _by = OwnerID
}

// SeedTenant writes an organization, three clients, one owner and two client users.
func SeedTenant(t *testing.T, pool *pgxpool.Pool, opts TenantOptions) Tenant {
	t.Helper()
	ctx := context.Background()
	maxInstances := opts.MaxInstances
	if maxInstances == 0 {
		maxInstances = 10
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	tn := Tenant{
		OrganizationID:  uuid.New(),
		DefaultClientID: uuid.New(),
		ClientA1:        uuid.New(),
		ClientA2:        uuid.New(),
		OwnerID:         uuid.New(),
		UserA1:          uuid.New(),
		UserA2:          uuid.New(),
		OwnerEmail:      "owner-" + suffix + "@test.example",
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("SeedTenant: %v", err)
		}
	}
	exec(`INSERT INTO organizations (id, name, max_instances, whatsapp_web_allowed) VALUES ($1, $2, $3, $4)`,
		tn.OrganizationID, opts.Name, maxInstances, opts.WhatsAppWebAllowed)
	client := func(id uuid.UUID, name string, isDefault, canCreate bool) {
		exec(`INSERT INTO clients (id, organization_id, name, is_default, can_create_instances) VALUES ($1, $2, $3, $4, $5)`,
			id, tn.OrganizationID, name, isDefault, canCreate)
	}
	client(tn.DefaultClientID, opts.Name+" default", true, false)
	client(tn.ClientA1, opts.Name+" A1", false, true)
	client(tn.ClientA2, opts.Name+" A2", false, true)
	exec(`INSERT INTO users (id, email, password_hash, organization_id, role) VALUES ($1, $2, 'x', $3, 'owner')`,
		tn.OwnerID, tn.OwnerEmail, tn.OrganizationID)
	user := func(id, clientID uuid.UUID, label string) {
		exec(`INSERT INTO users (id, email, password_hash, organization_id, role, client_id) VALUES ($1, $2, 'x', $3, 'client', $4)`,
			id, label+"-"+suffix+"@test.example", tn.OrganizationID, clientID)
	}
	user(tn.UserA1, tn.ClientA1, "a1")
	user(tn.UserA2, tn.ClientA2, "a2")
	if opts.Acknowledged {
		exec(`UPDATE organizations SET whatsapp_web_acknowledged_at = now(), whatsapp_web_acknowledged_by = $2 WHERE id = $1`,
			tn.OrganizationID, tn.OwnerID)
	}
	return tn
}

// InstanceSeed describes a row that SeedInstance writes. It exists so that recovery and watcher
// tests can create rows of any state and age without a pairing.
type InstanceSeed struct {
	OrganizationID, ClientID uuid.UUID
	CoreID                   string // "" = "main"
	Provider                 string // "" = "WHATSAPP_WEB"
	State                    string // "" = "active"
	LastStatus               *string
	SendOnly                 bool
	Enabled                  *bool     // nil = true
	PhoneNumberID            *string   // nil for META_CLOUD = "pn-" + the id's hex
	CreatedAt                time.Time // zero = time.Now()
}

// SeededInstance is the id and the core name of a seeded row.
type SeededInstance struct {
	ID       uuid.UUID
	CoreName string // "i_" + hex of ID
}

// SeedInstance writes one instance row with raw SQL. The display name is "Seeded". A WhatsApp Web
// row gets the locale es-ES, as every instance that the backend creates has one.
func SeedInstance(t *testing.T, pool *pgxpool.Pool, in InstanceSeed) SeededInstance {
	t.Helper()
	id := uuid.New()
	hex := strings.ReplaceAll(id.String(), "-", "")
	coreID, provider, state := in.CoreID, in.Provider, in.State
	if coreID == "" {
		coreID = "main"
	}
	if provider == "" {
		provider = "WHATSAPP_WEB"
	}
	if state == "" {
		state = "active"
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	phone := in.PhoneNumberID
	if provider == "META_CLOUD" && phone == nil {
		p := "pn-" + hex
		phone = &p
	}
	var locale *string
	if provider == "WHATSAPP_WEB" {
		l := "es-ES"
		locale = &l
	}
	created := in.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	_, err := pool.Exec(context.Background(), `
INSERT INTO instances (id, organization_id, client_id, core_id, core_name, display_name, provider,
                       send_only, enabled, phone_number_id, state, last_status, locale, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 'Seeded', $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
		id, in.OrganizationID, in.ClientID, coreID, "i_"+hex, provider, in.SendOnly, enabled, phone, state, in.LastStatus, locale, created)
	if err != nil {
		t.Fatalf("SeedInstance: %v", err)
	}
	return SeededInstance{ID: id, CoreName: "i_" + hex}
}

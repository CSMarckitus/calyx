// Package operator holds the operator commands of the backend: the commands that the operator of
// the installation runs in a terminal, next to a running server.
package operator

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/config"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/tenancy"
)

// Usage is the help text. cmd/backend prints it for "help" and for wrong usage.
const Usage = `Usage: backend <command> [flags]

Commands:
  serve                 Migrate the database, then run the server.
  migrate               Migrate the database, then stop.
  org create --name N --owner-email E [--max-instances K] [--whatsapp-web allowed|blocked]
  org set --id ID [--name N] [--max-instances K] [--whatsapp-web allowed|blocked]
  org set-owner --id ID --email E
  org list
  user reset-password --email E
  user disable --email E
  user enable --email E
  instances orphans
  mail test ADDRESS     Send a test mail with the configured SMTP.
  openapi               Print the OpenAPI document of the HTTP API.
  --version             Print the version of the backend.
  help                  Show this text.

Every command reads the same environment variables as the server, except openapi, help and --version.
`

// Deps is what the commands need.
type Deps struct {
	Config  config.Config
	Pool    *pgxpool.Pool
	Auth    *auth.Service
	Tenancy *tenancy.Service
	Core    *core.Client
	Mailer  mail.Sender
}

// run is the code of one command. It gets the parsed words after the command name.
type run func(c *cmdContext, args []string) int

type command struct {
	usage string // the usage line of this command
	run   run
}

var commands = map[string]command{
	"org create":          {"backend org create --name N --owner-email E [--max-instances K] [--whatsapp-web allowed|blocked]", orgCreate},
	"org set":             {"backend org set --id ID [--name N] [--max-instances K] [--whatsapp-web allowed|blocked]", orgSet},
	"org set-owner":       {"backend org set-owner --id ID --email E", orgSetOwner},
	"org list":            {"backend org list", orgList},
	"user reset-password": {"backend user reset-password --email E", userResetPassword},
	"user disable":        {"backend user disable --email E", userDisable},
	"user enable":         {"backend user enable --email E", userEnable},
	"instances orphans":   {"backend instances orphans", instancesOrphans},
	"mail test":           {"backend mail test ADDRESS", mailTest},
}

// cmdContext is the state of one command run.
type cmdContext struct {
	ctx    context.Context
	d      Deps
	out    io.Writer
	errOut io.Writer
	name   string
	usage  string
}

// usageError prints the reason and the usage line, and returns the exit code 2.
func (c *cmdContext) usageError(format string, a ...any) int {
	fmt.Fprintf(c.errOut, "Error: %s\n", fmt.Sprintf(format, a...))
	fmt.Fprintf(c.errOut, "Usage: %s\n", c.usage)
	return 2
}

// fail prints the error and returns the exit code 1. A 400 invalid_request of a service is a wrong
// value: fail prints its field errors and the usage line, and returns 2.
func (c *cmdContext) fail(err error) int {
	printError(c.errOut, err)
	if ae, ok := apierr.As(err); ok && ae.Code == apierr.CodeInvalidRequest {
		fmt.Fprintf(c.errOut, "Usage: %s\n", c.usage)
		return 2
	}
	return 1
}

// Run runs one operator command. args is the list after the name of the binary. It returns the
// exit code: 0 on success, 1 on an error, 2 on wrong usage.
func Run(ctx context.Context, args []string, d Deps, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, Usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, Usage)
		return 0
	}
	words := args[:1]
	switch args[0] {
	case "org", "user", "instances", "mail":
		if len(args) >= 2 {
			words = args[:2]
		}
	}
	name := strings.Join(words, " ")
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(stderr, "Error: unknown command %q.\n\n%s", name, Usage)
		return 2
	}
	c := &cmdContext{ctx: ctx, d: d, out: stdout, errOut: stderr, name: name, usage: cmd.usage}
	return cmd.run(c, args[len(words):])
}

// parse parses the flags of a command. It reports a usage error for an unknown flag or an
// argument that is not a flag. ok is false when the command must stop with code.
func (c *cmdContext) parse(args []string, define func(fs *flag.FlagSet)) (fs *flag.FlagSet, code int, ok bool) {
	fs = flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	define(fs)
	if err := fs.Parse(args); err != nil {
		return fs, c.usageError("%v", err), false
	}
	if fs.NArg() > 0 {
		return fs, c.usageError("unexpected argument %q", fs.Arg(0)), false
	}
	return fs, 0, true
}

func given(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// The flag checks below cover the flag syntax only. The services check the values.

func checkWeb(v string) (bool, error) {
	switch v {
	case "allowed":
		return true, nil
	case "blocked":
		return false, nil
	}
	return false, errors.New("--whatsapp-web must be allowed or blocked")
}

func checkID(v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, errors.New("--id must be a UUID")
	}
	return id, nil
}

func orgCreate(c *cmdContext, args []string) int {
	var name, email, web string
	var maxInstances int
	fs, code, ok := c.parse(args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "")
		fs.StringVar(&email, "owner-email", "", "")
		fs.IntVar(&maxInstances, "max-instances", 10, "")
		fs.StringVar(&web, "whatsapp-web", "", "")
	})
	if !ok {
		return code
	}
	// The email is checked first: a wrong email must not leave an organization without an owner.
	email, err := auth.NormalizeEmail(email)
	if err != nil {
		return c.fail(err)
	}
	allowed := c.d.Config.WhatsAppWebDefault
	if given(fs, "whatsapp-web") {
		if allowed, err = checkWeb(web); err != nil {
			return c.usageError("%v", err)
		}
	}
	org, _, err := c.d.Tenancy.CreateOrganization(c.ctx, tenancy.NewOrganization{Name: name, MaxInstances: maxInstances, WhatsAppWebAllowed: allowed})
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.out, "Organization created.")
	printOrganization(c.out, org)
	res, err := c.d.Auth.InviteOwner(c.ctx, org.ID, email, nil)
	if err != nil {
		printError(c.errOut, err)
		fmt.Fprintf(c.errOut, "The organization exists without an owner invitation. Run: backend org set-owner --id %s --email %s\n", org.ID, email)
		return 1
	}
	printInvitation(c.out, c.d.Config.PublicURL.String(), res)
	return 0
}

func orgSet(c *cmdContext, args []string) int {
	var id, name, web string
	var maxInstances int
	fs, code, ok := c.parse(args, func(fs *flag.FlagSet) {
		fs.StringVar(&id, "id", "", "")
		fs.StringVar(&name, "name", "", "")
		fs.IntVar(&maxInstances, "max-instances", 0, "")
		fs.StringVar(&web, "whatsapp-web", "", "")
	})
	if !ok {
		return code
	}
	orgID, err := checkID(id)
	if err != nil {
		return c.usageError("%v", err)
	}
	if !given(fs, "name") && !given(fs, "max-instances") && !given(fs, "whatsapp-web") {
		return c.usageError("give at least one of --name, --max-instances and --whatsapp-web")
	}
	var ch tenancy.OrganizationChange
	if given(fs, "name") {
		ch.Name = &name
	}
	if given(fs, "max-instances") {
		ch.MaxInstances = &maxInstances
	}
	if given(fs, "whatsapp-web") {
		allowed, err := checkWeb(web)
		if err != nil {
			return c.usageError("%v", err)
		}
		ch.WhatsAppWebAllowed = &allowed
	}
	org, err := c.d.Tenancy.UpdateOrganization(c.ctx, orgID, ch)
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.out, "Organization updated.")
	printOrganization(c.out, org)
	return 0
}

func orgSetOwner(c *cmdContext, args []string) int {
	var id, email string
	_, code, ok := c.parse(args, func(fs *flag.FlagSet) {
		fs.StringVar(&id, "id", "", "")
		fs.StringVar(&email, "email", "", "")
	})
	if !ok {
		return code
	}
	orgID, err := checkID(id)
	if err != nil {
		return c.usageError("%v", err)
	}
	email, err = auth.NormalizeEmail(email)
	if err != nil {
		return c.fail(err)
	}
	q := gen.New(c.d.Pool)
	var replaces *uuid.UUID
	var oldEmail string
	owner, err := q.OperatorGetActiveOwner(c.ctx, orgID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return c.fail(err)
	default:
		replaces, oldEmail = &owner.ID, owner.Email
	}
	res, err := c.d.Auth.InviteOwner(c.ctx, orgID, email, replaces)
	if err != nil {
		return c.fail(err)
	}
	printInvitation(c.out, c.d.Config.PublicURL.String(), res)
	if replaces != nil {
		fmt.Fprintf(c.out, "When the invitation is accepted, %s is disabled.\n", oldEmail)
	}
	return 0
}

func orgList(c *cmdContext, args []string) int {
	if _, code, ok := c.parse(args, func(*flag.FlagSet) {}); !ok {
		return code
	}
	orgs, err := c.d.Tenancy.ListOrganizations(c.ctx)
	if err != nil {
		return c.fail(err)
	}
	owners, err := gen.New(c.d.Pool).OperatorListActiveOwners(c.ctx)
	if err != nil {
		return c.fail(err)
	}
	if len(orgs) == 0 {
		fmt.Fprintln(c.out, "No organizations.")
		return 0
	}
	ownerOf := map[uuid.UUID]string{}
	for _, o := range owners {
		ownerOf[o.OrganizationID] = o.Email
	}
	t := newTable(c.out)
	fmt.Fprintln(t, "ID\tNAME\tINSTANCES\tMAX\tWHATSAPP WEB\tOWNER")
	for _, o := range orgs {
		owner := ownerOf[o.ID]
		if owner == "" {
			owner = "-"
		}
		fmt.Fprintf(t, "%s\t%s\t%d\t%d\t%s\t%s\n", o.ID, o.Name, o.InstanceCount, o.MaxInstances, webLabel(o.WhatsappWebAllowed), owner)
	}
	_ = t.Flush()
	return 0
}

// emailOnly parses the one flag --email of the user commands.
func emailOnly(c *cmdContext, args []string) (string, int, bool) {
	var email string
	_, code, ok := c.parse(args, func(fs *flag.FlagSet) { fs.StringVar(&email, "email", "", "") })
	if !ok {
		return "", code, false
	}
	// The normalized email is the one the output names.
	email, err := auth.NormalizeEmail(email)
	if err != nil {
		return "", c.fail(err), false
	}
	return email, 0, true
}

func userResetPassword(c *cmdContext, args []string) int {
	email, code, ok := emailOnly(c, args)
	if !ok {
		return code
	}
	link, err := c.d.Auth.CreatePasswordReset(c.ctx, email)
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "Reset link for %s (valid for 1 hour):\n%s\n", email, link)
	printCurl(c.out, c.d.Config.PublicURL.String(), "/api/v1/auth/password-reset/confirm", link)
	return 0
}

func userDisable(c *cmdContext, args []string) int {
	email, code, ok := emailOnly(c, args)
	if !ok {
		return code
	}
	if err := c.d.Auth.SetUserDisabled(c.ctx, email, true); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "User %s is disabled. Their sessions were deleted and their API keys revoked.\n", email)
	return 0
}

func userEnable(c *cmdContext, args []string) int {
	email, code, ok := emailOnly(c, args)
	if !ok {
		return code
	}
	if err := c.d.Auth.SetUserDisabled(c.ctx, email, false); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.out, "User %s is enabled.\n", email)
	return 0
}

func instancesOrphans(c *cmdContext, args []string) int {
	if _, code, ok := c.parse(args, func(*flag.FlagSet) {}); !ok {
		return code
	}
	inCore, err := c.d.Core.ListInstances(c.ctx)
	if err != nil {
		return c.fail(err)
	}
	names, err := gen.New(c.d.Pool).OperatorListCoreNames(c.ctx, c.d.Config.CoreID)
	if err != nil {
		return c.fail(err)
	}
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	var orphans []core.InstanceInfo
	for _, i := range inCore {
		if !known[i.CoreName] {
			orphans = append(orphans, i)
		}
	}
	if len(orphans) == 0 {
		fmt.Fprintln(c.out, "No orphan instances.")
		return 0
	}
	t := newTable(c.out)
	fmt.Fprintln(t, "CORE NAME\tPROVIDER\tSTATUS")
	for _, i := range orphans {
		fmt.Fprintf(t, "%s\t%s\t%s\n", i.CoreName, i.Provider, i.Status)
	}
	_ = t.Flush()
	return 0
}

func mailTest(c *cmdContext, args []string) int {
	if len(args) != 1 {
		return c.usageError("give one address")
	}
	to, err := auth.NormalizeEmail(args[0])
	if err != nil {
		return c.usageError("%q is not a valid email address", args[0])
	}
	err = c.d.Mailer.Send(c.ctx, mail.Message{
		To: to, Subject: "Calyx test mail",
		Body: "This is a test mail from the Calyx backend.\nYour SMTP settings work.\n",
	})
	if errors.Is(err, mail.ErrNotConfigured) {
		fmt.Fprintln(c.errOut, "Error: mail is not configured. Set SMTP_HOST, SMTP_FROM and OPERATOR_EMAIL.")
		return 1
	}
	if err != nil {
		return c.fail(err)
	}
	fmt.Fprintln(c.out, "sent")
	return 0
}

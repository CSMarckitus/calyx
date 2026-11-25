package operator

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/database/gen"
)

// The output of the commands: plain text. Times are RFC 3339 in UTC.

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func webLabel(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "blocked"
}

// printOrganization writes the four lines that describe an organization.
func printOrganization(w io.Writer, o gen.Organization) {
	fmt.Fprintf(w, "id: %s\n", o.ID)
	fmt.Fprintf(w, "name: %s\n", o.Name)
	fmt.Fprintf(w, "max instances: %d\n", o.MaxInstances)
	fmt.Fprintf(w, "WhatsApp Web: %s\n", webLabel(o.WhatsappWebAllowed))
}

// printInvitation writes the lines of an owner invitation. The link is only printed when the mail
// was not sent.
func printInvitation(w io.Writer, publicURL string, r auth.InvitationResult) {
	fmt.Fprintf(w, "Owner invitation for %s, valid until %s.\n", r.Email, rfc3339(r.ExpiresAt))
	if r.MailSent {
		fmt.Fprintln(w, "The invitation mail was sent.")
		return
	}
	fmt.Fprintln(w, "The invitation mail was not sent. Give this link to the owner:")
	fmt.Fprintln(w, r.Link)
	printCurl(w, publicURL, "/api/v1/invitations/accept", r.Link)
}

// printCurl writes the curl command that uses the token of a link, for an installation without a
// frontend. The token has only URL-safe characters, so the quotes need no escape.
func printCurl(w io.Writer, publicURL, path, link string) {
	var token string
	if u, err := url.Parse(link); err == nil {
		token = u.Query().Get("token")
	}
	fmt.Fprintln(w, "Without a frontend, run:")
	fmt.Fprintf(w, `curl -X POST %s%s -H 'Content-Type: application/json' -d '{"token":"%s","password":"<new password>"}'`+"\n", publicURL, path, token)
}

// newTable returns a writer that aligns columns with two spaces between them.
func newTable(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }

// errorText is the text after "Error: ". It is the detail of an apierr, or err.Error(). An
// internal error shows its cause: the output goes to the terminal of the operator.
func errorText(err error) string {
	if ae, ok := apierr.As(err); ok {
		if cause := ae.Unwrap(); cause != nil && ae.Code == apierr.CodeInternal {
			return ae.Detail + " " + cause.Error()
		}
		return ae.Detail
	}
	return err.Error()
}

// printError writes "Error: <text>", and a hint when a table does not exist (SQLSTATE 42P01). An
// error with field errors writes one line "Error: <field>: <message>" for each of them.
func printError(w io.Writer, err error) {
	if ae, ok := apierr.As(err); ok && len(ae.Errors) > 0 {
		for _, fe := range ae.Errors {
			fmt.Fprintf(w, "Error: %s: %s\n", fe.Field, fe.Message)
		}
		return
	}
	fmt.Fprintf(w, "Error: %s\n", errorText(err))
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "42P01" {
		fmt.Fprintln(w, "The database is not migrated. Run: backend migrate")
	}
}

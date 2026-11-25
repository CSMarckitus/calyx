//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"calyx/backend/internal/core"
	"calyx/backend/internal/testutil"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	textMessage    = "E2E-TEXT-7f3a"
	textMessage2   = "E2E-TEXT-2b9c"
	bulkMessage    = "E2E-BULK-9c1d"
	mcpTextMessage = "E2E-MCP-TEXT-5c1d"
	mcpIdemKey     = "e2e-mcp-1"
	ownerPass      = "Owner-Password-123"
	clientPass     = "Client-Password-456"
)

// api sends one JSON request. With key != "" it sends a Bearer token. Otherwise it uses the cookie
// jar of c and the Origin header. It returns the status and the decoded body (nil when not JSON).
func (h *harness) api(c *http.Client, key, method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.backendURL+path, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("Origin", h.backendURL)
	}
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v\n%s", method, path, err, h.logs())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// mailTo waits up to limit for the n-th mail to addr (n counts from 1) and returns it.
func (h *harness) mailTo(addr string, n int, limit time.Duration) testutil.ReceivedMail {
	h.t.Helper()
	var found testutil.ReceivedMail
	waitFor(h.t, "mail number "+itoa(n)+" to "+addr, limit, func() bool {
		count := 0
		for _, m := range h.smtp.Messages() {
			for _, to := range m.To {
				if strings.EqualFold(to, addr) {
					count++
					if count == n {
						found = m
						return true
					}
				}
			}
		}
		return false
	}, h.logs)
	return found
}

func (h *harness) mailCount(addr string) int {
	count := 0
	for _, m := range h.smtp.Messages() {
		for _, to := range m.To {
			if strings.EqualFold(to, addr) {
				count++
			}
		}
	}
	return count
}

// mcp sends one JSON-RPC request to /mcp with the API key and returns the "result" object.
func (h *harness) mcp(key, method string, params any) map[string]any {
	h.t.Helper()
	status, body := h.api(&http.Client{Timeout: 60 * time.Second}, key, "POST", "/mcp",
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if status != 200 {
		h.t.Fatalf("mcp %s: status %d, body %v\n%s", method, status, body, h.logs())
	}
	result, _ := body["result"].(map[string]any)
	if result == nil {
		h.t.Fatalf("mcp %s: no result in %v\n%s", method, body, h.logs())
	}
	return result
}

// toolResult calls one tool and decodes the JSON text of its answer. It fails on a tool error.
func (h *harness) toolResult(key, name string, args map[string]any) map[string]any {
	h.t.Helper()
	result := h.mcp(key, "tools/call", map[string]any{"name": name, "arguments": args})
	content, _ := result["content"].([]any)
	if result["isError"] == true || len(content) == 0 {
		h.t.Fatalf("tool %s failed: %v\n%s", name, result, h.logs())
	}
	var out map[string]any
	text := str(content[0].(map[string]any), "text")
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		h.t.Fatalf("tool %s: the text is not JSON: %v", name, err)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

// linkIn returns the first link of a mail body that starts with the public URL and the path.
func linkIn(t *testing.T, publicURL string, m testutil.ReceivedMail, path string) string {
	t.Helper()
	_, body, ok := strings.Cut(m.Data, "\r\n\r\n")
	if !ok {
		t.Fatalf("the mail has no blank line between headers and body:\n%s", m.Data)
	}
	re := regexp.MustCompile(regexp.QuoteMeta(publicURL+path) + `\?token=[A-Za-z0-9_-]+`)
	link := re.FindString(body)
	if link == "" {
		t.Fatalf("no %s link in the mail body:\n%s", path, body)
	}
	return link
}

func newClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 60 * time.Second}
}

func need(t *testing.T, h *harness, step string, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("step %s: status %d, want %d, body %v\n%s", step, got, want, body, h.logs())
	}
}

func str(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

func cookieValues(c *http.Client, base string) []string {
	u, _ := url.Parse(base)
	var out []string
	for _, ck := range c.Jar.Cookies(u) {
		out = append(out, ck.Value)
	}
	return out
}

func TestBackendEndToEnd(t *testing.T) {
	h := newHarness(t)
	started := time.Now()
	step := func(n int, what string) {
		t.Logf("step %d (+%s): %s", n, time.Since(started).Round(time.Millisecond), what)
	}
	var secrets []string // values that must never reach the logs

	// 1. Create the organization with the operator command.
	step(1, "operator org create")
	stdout, stderr, code := h.operator("org", "create", "--name", "E2E Org", "--owner-email", "owner@e2e.test")
	if code != 0 || !strings.Contains(stdout, "Organization created.") || !strings.Contains(stdout, "The invitation mail was sent.") || strings.Contains(stdout, "token=") {
		t.Fatalf("step 1: code %d\nstdout: %s\nstderr: %s\n%s", code, stdout, stderr, h.logs())
	}

	// 2. The invitation mail.
	step(2, "invitation mail")
	m := h.mailTo("owner@e2e.test", 1, 10*time.Second)
	if !strings.Contains(m.Data, "Subject: You are invited to manage E2E Org on Calyx") {
		t.Fatalf("step 2: unexpected mail:\n%s", m.Data)
	}
	link := linkIn(t, h.backendURL, m, "/invite")
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/invite\?token=[A-Za-z0-9_-]{43}$`).MatchString(link) {
		t.Fatalf("step 2: the link %q has the wrong form", link)
	}
	_, ownerInviteToken, _ := strings.Cut(link, "token=")
	secrets = append(secrets, ownerInviteToken)

	// 3. Without a frontend, the link tells how to accept with the API.
	step(3, "invitation link without a frontend")
	plain := &http.Client{Timeout: 30 * time.Second}
	resp, err := plain.Get(link)
	if err != nil {
		t.Fatalf("step 3: %v\n%s", err, h.logs())
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") ||
		!strings.Contains(string(page), "(acceptInvitation)") || strings.Contains(string(page), ownerInviteToken) {
		t.Fatalf("step 3: status %d, type %q\n%s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), page, h.logs())
	}

	// 4. Accept the invitation through the API (acceptInvitation).
	step(4, "accept the invitation (API)")
	accepter := newClient(t)
	status, body := h.api(accepter, "", "POST", "/api/v1/invitations/accept", map[string]any{"token": ownerInviteToken, "password": ownerPass})
	need(t, h, "4", status, 200, body)
	if body["role"] != "owner" {
		t.Fatalf("step 4: role = %v, body %v", body["role"], body)
	}
	secrets = append(secrets, ownerPass)
	secrets = append(secrets, cookieValues(accepter, h.backendURL)...)

	// 5. Log in as the owner.
	step(5, "owner login")
	owner := newClient(t)
	status, body = h.api(owner, "", "POST", "/api/v1/auth/login", map[string]any{"email": "owner@e2e.test", "password": ownerPass})
	need(t, h, "5", status, 200, body)
	if body["role"] != "owner" {
		t.Fatalf("step 5: role = %v, body %v", body["role"], body)
	}
	if len(cookieValues(owner, h.backendURL)) == 0 {
		t.Fatalf("step 5: the jar has no cookie\n%s", h.logs())
	}
	if c := cookieValues(owner, h.backendURL); len(c) > 0 {
		secrets = append(secrets, c...)
	}

	// 6. Create the client Shop.
	step(6, "create client")
	status, body = h.api(owner, "", "POST", "/api/v1/clients", map[string]any{"name": "Shop", "can_create_instances": true, "max_instances": 2})
	need(t, h, "6", status, 201, body)
	if body["is_default"] != false {
		t.Fatalf("step 6: is_default = %v, body %v", body["is_default"], body)
	}
	shopID := str(body, "id")

	// 7. Invite the user of the client.
	step(7, "invite the client user")
	status, body = h.api(owner, "", "POST", "/api/v1/clients/"+shopID+"/invitations", map[string]any{"email": "shop@e2e.test"})
	need(t, h, "7", status, 201, body)
	if body["mail_sent"] != true || body["invite_link"] != nil {
		t.Fatalf("step 7: body %v", body)
	}

	// 8. The client user accepts through the API.
	step(8, "client user accepts")
	m = h.mailTo("shop@e2e.test", 1, 10*time.Second)
	link = linkIn(t, h.backendURL, m, "/invite")
	shopToken := link[strings.Index(link, "token=")+len("token="):]
	secrets = append(secrets, shopToken, clientPass)
	shop := newClient(t)
	status, body = h.api(shop, "", "POST", "/api/v1/invitations/accept", map[string]any{"token": shopToken, "password": clientPass})
	need(t, h, "8", status, 200, body)
	if body["role"] != "client" || str(body, "client_id") != shopID {
		t.Fatalf("step 8: body %v, want role client and client_id %s", body, shopID)
	}
	secrets = append(secrets, cookieValues(shop, h.backendURL)...)

	// 9. The client user creates an API key.
	step(9, "create API key")
	status, body = h.api(shop, "", "POST", "/api/v1/api-keys", map[string]any{"name": "n8n"})
	need(t, h, "9", status, 201, body)
	key := str(body, "token")
	if !regexp.MustCompile(`^cx_[A-Za-z0-9_-]{43}$`).MatchString(key) {
		t.Fatalf("step 9: the key has the wrong form (%d chars)", len(key))
	}
	secrets = append(secrets, key)

	// 10. Create a Meta Cloud instance with the key.
	step(10, "create Meta instance")
	apiClient := &http.Client{Timeout: 60 * time.Second}
	status, body = h.api(apiClient, key, "POST", "/api/v1/instances", map[string]any{
		"display_name": "Shop Meta", "provider": "META_CLOUD",
		"meta_credentials": map[string]any{"phone_number_id": "123456789", "access_token": "good-token", "app_secret": "app-secret"},
	})
	need(t, h, "10", status, 201, body)
	if str(body, "instance", "client_id") != shopID || str(body, "instance", "state") != "active" || str(body, "pairing", "status") != "CONNECTED" {
		t.Fatalf("step 10: body %v", body)
	}
	if !h.graph.has("GET", "/v23.0/123456789") {
		t.Fatalf("step 10: the fake Graph API did not receive GET /v23.0/123456789\n%s", h.logs())
	}
	instID := str(body, "instance", "id")
	secrets = append(secrets, "good-token", "app-secret")

	// 11. Send one message, then a wrong one: every wrong field comes in errors[].
	step(11, "send a message")
	status, body = h.api(apiClient, key, "POST", "/api/v1/instances/"+instID+"/messages", map[string]any{"to": "34600111222", "text": textMessage})
	need(t, h, "11", status, 200, body)
	sends := h.graph.sends()
	if body["message_id"] != "wamid.1" || len(sends) < 1 || sends[0].Body["to"] != "34600111222" || str(sends[0].Body, "text", "body") != textMessage {
		t.Fatalf("step 11: body %v, sends %v", body, sends)
	}
	status, body = h.api(apiClient, key, "POST", "/api/v1/instances/"+instID+"/messages", map[string]any{"to": "", "text": " "})
	need(t, h, "11", status, 400, body)
	if errs, _ := body["errors"].([]any); body["code"] != "invalid_request" || len(errs) != 2 ||
		str(errs[0].(map[string]any), "location") != "body.to" || str(errs[1].(map[string]any), "location") != "body.text" {
		t.Fatalf("step 11: the wrong send got %v", body)
	}

	// 12. Create a bulk job.
	step(12, "create bulk job")
	status, body = h.api(apiClient, key, "POST", "/api/v1/instances/"+instID+"/bulk-jobs", map[string]any{
		"recipients": []string{"34600000001", "34600000002", "34600000003"}, "text": bulkMessage,
	})
	need(t, h, "12", status, 201, body)
	if body["total_messages"] != float64(3) || str(body, "instance_id") != instID {
		t.Fatalf("step 12: body %v", body)
	}
	jobID := str(body, "id")

	// 13. Wait for the job.
	step(13, "wait for the bulk job")
	var job map[string]any
	waitFor(t, "the bulk job is COMPLETED", 30*time.Second, func() bool {
		_, job = h.api(apiClient, key, "GET", "/api/v1/bulk-jobs/"+jobID, nil)
		return job["status"] == "COMPLETED"
	}, h.logs)
	if job["sent_messages"] != float64(3) || job["progress_percent"] != float64(100) {
		t.Fatalf("step 13: job %v", job)
	}
	status, body = h.api(apiClient, key, "GET", "/api/v1/instances/"+instID+"/bulk-jobs", nil)
	items, _ := body["items"].([]any)
	if status != 200 || len(items) != 1 {
		t.Fatalf("step 13: list status %d, body %v", status, body)
	}
	if n := len(h.graph.sends()); n != 4 {
		t.Fatalf("step 13: the fake Graph API has %d sends, want 4", n)
	}

	// 13a to 13e. The MCP server, with the API key of step 9.
	step(13, "MCP: list the tools")
	result := h.mcp(key, "tools/list", map[string]any{})
	tools, _ := result["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, str(tool.(map[string]any), "name"))
	}
	sort.Strings(names)
	wantTools := []string{"cancel_bulk_job", "create_bulk_job", "get_bulk_job", "get_instance", "list_instances", "send_message"}
	if !slices.Equal(names, wantTools) {
		t.Fatalf("step 13b: tools %v, want %v", names, wantTools)
	}

	step(13, "MCP: send a message")
	mcpArgs := map[string]any{"id": instID, "to": "34600111222", "text": mcpTextMessage, "Idempotency-Key": mcpIdemKey}
	first := h.toolResult(key, "send_message", mcpArgs)
	firstID := str(first, "message_id")
	if firstID == "" || first["replayed"] == true {
		t.Fatalf("step 13c: answer %v", first)
	}

	step(13, "MCP: repeat the send")
	again := h.toolResult(key, "send_message", mcpArgs)
	if again["replayed"] != true || str(again, "message_id") != firstID {
		t.Fatalf("step 13d: answer %v, want replayed true and message_id %s", again, firstID)
	}

	if n := len(h.graph.sends()); n != 5 {
		t.Fatalf("step 13e: the fake Graph API has %d sends, want 5", n)
	}

	// 14. Rotate the token in the fake: the next send fails.
	step(14, "send with a rotated token")
	h.graph.setValidToken("rotated-token")
	status, body = h.api(apiClient, key, "POST", "/api/v1/instances/"+instID+"/messages", map[string]any{"to": "34600111222", "text": textMessage2})
	need(t, h, "14", status, 409, body)
	if body["code"] != "instance_not_ready" {
		t.Fatalf("step 14: body %v", body)
	}

	// 15. The alert mail reaches the owner.
	step(15, "alert mail")
	m = h.mailTo("owner@e2e.test", 2, 15*time.Second)
	if !strings.Contains(m.Data, "Subject: [Calyx] Shop Meta needs new Meta credentials") {
		t.Fatalf("step 15: unexpected mail (a different kind of alert?):\n%s", m.Data)
	}
	_, mailBody, _ := strings.Cut(m.Data, "\r\n\r\n")
	if !strings.Contains(mailBody, "Shop Meta") || !strings.Contains(mailBody, "Shop") {
		t.Fatalf("step 15: the mail body does not name the instance and the client:\n%s", mailBody)
	}

	// 16. The instance shows the alert.
	step(16, "instance shows the alert")
	status, body = h.api(apiClient, key, "GET", "/api/v1/instances/"+instID, nil)
	need(t, h, "16", status, 200, body)
	alert, _ := body["alert"].(map[string]any)
	if body["status"] != "NEEDS_AUTH" || alert == nil || alert["kind"] != "needs_auth" || alert["emailed_at"] == nil {
		t.Fatalf("step 16: body %v", body)
	}

	// 17. No second alert in three check intervals.
	step(17, "no second alert")
	time.Sleep(3 * time.Second) // the only sleep without a condition: the window of the "no second alert" check
	if n := h.mailCount("owner@e2e.test"); n != 2 {
		var subjects []string
		for _, mm := range h.smtp.Messages() {
			if i := strings.Index(mm.Data, "Subject:"); i >= 0 {
				subjects = append(subjects, strings.SplitN(mm.Data[i:], "\r\n", 2)[0])
			}
		}
		t.Fatalf("step 17: the owner has %d mails, want 2. Subjects: %v\n%s", n, subjects, h.logs())
	}

	// 18. No orphans.
	step(18, "operator instances orphans")
	stdout, stderr, code = h.operator("instances", "orphans")
	if code != 0 || strings.TrimSpace(stdout) != "No orphan instances." {
		t.Fatalf("step 18: code %d, stdout %q, stderr %q\n%s", code, stdout, stderr, h.logs())
	}

	// 19. The served OpenAPI document is the committed copy.
	step(19, "the OpenAPI document")
	resp, err = plain.Get(h.backendURL + "/openapi.json")
	if err != nil {
		t.Fatalf("step 19: %v\n%s", err, h.logs())
	}
	served, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	committed, err := os.ReadFile(filepath.Join(h.root, "packages", "shared-types", "openapi", "backend.json"))
	if err != nil {
		t.Fatalf("step 19: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(served, committed) {
		t.Fatalf("step 19: status %d; the served document is not the committed copy: run pnpm --filter @calyx/backend openapi:generate", resp.StatusCode)
	}

	// 20. Stop the backend.
	step(20, "stop the backend")
	h.stop(h.backend, 15*time.Second)
	if h.backend.err != nil {
		t.Fatalf("step 20: the backend exited with %v\n%s", h.backend.err, h.logs())
	}
	if !strings.Contains(h.backend.out.String(), "Shutdown complete.") {
		t.Fatalf("step 20: no \"Shutdown complete.\" in the backend log\n%s", h.logs())
	}

	// 21. The log holds no content and no secret.
	step(21, "scan the backend log")
	secrets = append(secrets, textMessage, textMessage2, bulkMessage, "34600111222", "34600000001", mcpTextMessage, mcpIdemKey, coreToken, secretKey)
	logText := h.backend.out.String()
	for _, s := range secrets {
		if s != "" && strings.Contains(logText, s) {
			t.Errorf("step 21: the backend log contains a value that must not be logged (%d chars, starts %q)", len(s), prefix(s))
		}
	}
	t.Logf("scenario took %s", time.Since(started))
}

func prefix(s string) string {
	if len(s) > 4 {
		return s[:4] + "..."
	}
	return s
}

// TestLiveStateMeasures measures goals 1 and 4 of the live state. The backend does a round each
// 10 minutes, so only the core link can explain a fast change.
func TestLiveStateMeasures(t *testing.T) {
	h := newHarnessWithRounds(t, "10m")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// An owner with a session and a Meta Cloud instance.
	stdout, stderr, code := h.operator("org", "create", "--name", "Live Org", "--owner-email", "owner@e2e.test")
	if code != 0 {
		t.Fatalf("org create: code %d\nstdout: %s\nstderr: %s\n%s", code, stdout, stderr, h.logs())
	}
	link := linkIn(t, h.backendURL, h.mailTo("owner@e2e.test", 1, 10*time.Second), "/invite")
	owner := newClient(t)
	status, body := h.api(owner, "", "POST", "/api/v1/invitations/accept",
		map[string]any{"token": link[strings.Index(link, "token=")+len("token="):], "password": ownerPass})
	need(t, h, "accept", status, 200, body)
	status, body = h.api(owner, "", "POST", "/api/v1/instances", map[string]any{
		"display_name": "Live Meta", "provider": "META_CLOUD",
		"meta_credentials": map[string]any{"phone_number_id": "123456789", "access_token": "good-token", "app_secret": "app-secret"},
	})
	need(t, h, "create instance", status, 201, body)
	instID := str(body, "instance", "id")

	conn, err := core.Dial(h.coreAddr, coreToken)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	list, err := corev1.NewInstanceServiceClient(conn).ListInstances(ctx, &corev1.ListInstancesRequest{})
	if err != nil || len(list.GetInstances()) != 1 {
		t.Fatalf("ListInstances: %v, %v\n%s", list, err, h.logs())
	}
	coreName := list.GetInstances()[0].GetInstanceName()

	// Goal 1. The own subscription of the test gives the time of the core event.
	subCtx, endSub := context.WithCancel(ctx)
	defer endSub()
	sub, err := corev1.NewEventServiceClient(conn).Subscribe(subCtx, &corev1.SubscribeRequest{EventTypes: []string{"connection.update"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Header(); err != nil { // the core registered the subscription
		t.Fatalf("Subscribe headers: %v\n%s", err, h.logs())
	}
	changed := h.followEvents(ctx, owner)
	select {
	case <-changed: // the first changed of each stream
	case <-time.After(10 * time.Second):
		t.Fatalf("goal 1: no first changed\n%s", h.logs())
	}

	// The core publishes the event inside SendPlainText, so both readers run before the call.
	needsAuth := make(chan time.Time, 1) // occurred_at of the NEEDS_AUTH event
	subErr := make(chan error, 1)
	go func() {
		for {
			ev, err := sub.Recv()
			if err != nil {
				subErr <- err
				return
			}
			if ev.GetInstanceName() == coreName && strings.Contains(ev.GetPayloadJson(), "NEEDS_AUTH") {
				needsAuth <- ev.GetOccurredAt().AsTime()
				return
			}
		}
	}()
	h.graph.setValidToken("rotated-token")
	actionAt := time.Now()
	_, err = corev1.NewMessageServiceClient(conn).SendPlainText(ctx,
		&corev1.SendPlainTextRequest{InstanceName: coreName, To: "34600111222", Text: textMessage})
	if err == nil {
		t.Fatalf("goal 1: SendPlainText with a rotated token succeeded\n%s", h.logs())
	}
	var eventAt time.Time
	select {
	case eventAt = <-needsAuth:
	case err := <-subErr:
		t.Fatalf("goal 1: Subscribe: %v\n%s", err, h.logs())
	case <-time.After(10 * time.Second):
		t.Fatalf("goal 1: no NEEDS_AUTH event within 10 s\n%s", h.logs())
	}
	var changedAt time.Time
	deadline := time.After(10 * time.Second)
	for changedAt.Before(actionAt) {
		select {
		case changedAt = <-changed:
		case <-deadline:
			t.Fatalf("goal 1: no changed within 10 s of SendPlainText\n%s", h.logs())
		}
	}
	delay := changedAt.Sub(eventAt)
	t.Logf("goal 1: changed came %s after the core event (occurred_at)", delay)
	if delay < 0 || delay > 2*time.Second {
		t.Errorf("goal 1: changed came %s after the core event, want 0 to 2 s", delay)
	}
	status, body = h.api(owner, "", "GET", "/api/v1/instances/"+instID, nil)
	need(t, h, "read the instance", status, 200, body)
	if body["status"] != "NEEDS_AUTH" {
		t.Fatalf("goal 1: status %v after changed, want NEEDS_AUTH\n%s", body["status"], h.logs())
	}
	endSub()

	// Goal 4. The time counts from the signal that stops the core.
	if c := h.health()["core"]; c != "ok" {
		t.Fatalf("goal 4: core is %v before the signal, want ok\n%s", c, h.logs())
	}
	signalAt := time.Now()
	if err := h.core.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "core unreachable in /healthz", 10*time.Second, func() bool { return h.health()["core"] == "unreachable" }, h.logs)
	delay = time.Since(signalAt)
	t.Logf("goal 4: core unreachable %s after SIGTERM", delay)
	if delay > 5*time.Second {
		t.Errorf("goal 4: core unreachable %s after SIGTERM, want at most 5 s", delay)
	}
	select {
	case <-h.core.done:
		if h.core.err != nil {
			t.Fatalf("goal 4: the core exited with %v after SIGTERM\n%s", h.core.err, h.logs())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("goal 4: the core did not stop within 15 s after SIGTERM\n%s", h.logs())
	}

	h.startCore()
	restartAt := time.Now()
	waitFor(t, "core ok in /healthz after the restart", 30*time.Second, func() bool { return h.health()["core"] == "ok" }, h.logs)
	t.Logf("goal 4: core ok %s after the restart", time.Since(restartAt))
}

// followEvents opens GET /api/v1/events with the cookie of c. The channel gets the time of each
// changed. The stream ends with ctx.
func (h *harness) followEvents(ctx context.Context, c *http.Client) <-chan time.Time {
	h.t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.backendURL+"/api/v1/events", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Origin", h.backendURL)
	stream := &http.Client{Jar: c.Jar} // no timeout: the stream stays open
	resp, err := stream.Do(req)        //nolint:bodyclose // the goroutine below closes the body
	if err != nil {
		h.t.Fatalf("GET /api/v1/events: %v\n%s", err, h.logs())
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		h.t.Fatalf("GET /api/v1/events: status %d\n%s", resp.StatusCode, h.logs())
	}
	out := make(chan time.Time, 16)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if sc.Text() == "event: changed" {
				select {
				case out <- time.Now():
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

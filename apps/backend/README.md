# Calyx backend

The backend is the HTTP API of Calyx. It sits in front of the [core](../core/README.md) and adds
people, tenants and access control. New to Calyx? Read the [main README](../../README.md) first.

What the backend adds to the core:

- 👥 **People and tenants.** Organizations, the clients of each organization and their users,
  isolated from each other.
- 🔑 **Access.** A login for the panel, and API keys for your programs.
- 🔔 **Alerts.** A mail when a number disconnects.
- 📨 **Sends and bulk sends** through the core. The core does the pacing, and the backend cannot
  make it shorter.
- 🤖 **An MCP server** for AI agents.
- 📖 **An OpenAPI document** of every operation.

> [!NOTE]
> Status: alpha. Run one backend process for each database. The backend is open source
> (AGPL-3.0-only), with the isolation between tenants included. The API description
> (`packages/shared-types/openapi/backend.json`) is Apache-2.0, because it is a contract, like the
> `.proto` files of the core.

## Warning: WhatsApp Web

WhatsApp Web instances use an unofficial client (whatsmeow). Using it is against the Terms of
Service of WhatsApp. WhatsApp can ban the number.

Calyx limits how fast messages go out. This lowers the risk. It does not remove the risk.

An organization admin must accept this text once, before the first WhatsApp Web instance. The
panel shows the first two paragraphs above. The server admin can block WhatsApp Web for an
organization.

Meta Cloud instances use the official WhatsApp Business Cloud API, and the rules of Meta apply.
For example, free-form text is allowed only inside the 24-hour window. Templates are not
supported yet.

## Contents

**Get started**

- [Warning: WhatsApp Web](#warning-whatsapp-web)
- [Words in this document](#words-in-this-document)
- [Install with a domain (production)](#install-with-a-domain-production)
- [Install without a domain](#install-without-a-domain)
- [Send your first message](#send-your-first-message)

**Use the API**

- [The HTTP API](#the-http-api)
- [Log in and API keys](#log-in-and-api-keys)
- [Errors](#errors)
- [People and permissions](#people-and-permissions)
- [Pair a WhatsApp Web instance](#pair-a-whatsapp-web-instance)
- [Send messages](#send-messages)
- [Bulk jobs](#bulk-jobs)
- [Webhooks of instances](#webhooks-of-instances)
- [The live state and the health check](#the-live-state-and-the-health-check)
- [MCP server for AI agents](#mcp-server-for-ai-agents)

**Run and maintain**

- [Configuration](#configuration)
- [Operator commands](#operator-commands)
- [Alerts](#alerts)
- [Mail](#mail)
- [Backups, upgrades and rollback](#backups-upgrades-and-rollback)
- [What the database holds](#what-the-database-holds)

**[Development](#development)**

---

# Get started

## Words in this document

| Word | Meaning |
|---|---|
| **Server admin (operator)** | The person who runs the installation. The operator uses the command line, not the API. |
| **Organization** | One company. It has a limit on the number of instances. |
| **Organization admin (owner)** | The user who manages an organization, its clients and all its instances. |
| **Client** | A customer of the organization. The default client, "My numbers", holds the instances of the owner. |
| **Client user** | The one user of a client. This user manages the instances and the bulk jobs of that client only. |
| **Instance** | One WhatsApp number connected to the core. |
| **Provider** | How an instance connects: WhatsApp Web (`WHATSAPP_WEB`) or Meta Cloud (`META_CLOUD`). |
| **Pairing** | The link between a WhatsApp Web instance and a phone, with a QR code or an 8-character code. |
| **Re-pair** | A new pairing after an instance lost its link (status `NEEDS_AUTH`). |
| **Send-only instance** | An instance that connects only to send. It has no webhooks. |
| **Pacing** | The delays and pauses between messages. The core enforces them. |

The API, the command line and the technical documents use the short names "operator" and
"owner".

## Install with a domain (production)

This setup runs the core, the backend, the panel, PostgreSQL and Traefik (HTTPS) on one server.

You need:

- A Linux server with Docker and the compose plugin.
- A domain name whose DNS record points to the server.
- Ports 80 and 443 open. Let's Encrypt uses the HTTP challenge on port 80.

> [!CAUTION]
> Always name the services in a `docker compose` command. A `docker compose up` without service
> names starts every service of the active profiles.

1. Copy `.env.example` to `.env` in the repository root.
2. Set `CALYX_DOMAIN` (for example `api.example.com`) and `ACME_EMAIL`.
3. Set `POSTGRES_PASSWORD` to a value that is safe in a URL. Command: `openssl rand -hex 24`.
4. Set `CORE_TOKEN`. Command: `openssl rand -hex 32`.
5. Set `SECRET_KEY`. Command: `openssl rand -base64 32`.
6. Set the mail variables (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`)
   and `OPERATOR_EMAIL`. Or leave `SMTP_HOST` empty to run without mail.
7. Optional, for Meta Cloud instances: set `META_WEBHOOK_PORT` (for example `8081`) and
   `CORE_META_VERIFY_TOKEN`. See [Meta Cloud webhook](#meta-cloud-webhook).
8. Start the services:

   ```bash
   docker compose --profile backend up -d --build traefik frontend postgres core backend
   ```

9. Check the installation:

   ```bash
   curl https://<CALYX_DOMAIN>/healthz
   ```

   The answer is `{"database":"ok","core":"ok","mail":"configured",...}`. Without mail, `mail` is
   `off`. Right after the start, `core` can be `unknown` for a few seconds.

10. Create the first organization and its owner:

    ```bash
    docker compose --profile backend exec backend backend org create --name "My company" --owner-email you@example.com
    ```

The panel is now at `https://<CALYX_DOMAIN>`. Continue with
[Send your first message](#send-your-first-message).

<details>
<summary><b>How Traefik routes the requests, and other details of this setup</b></summary>

- Traefik gets and renews the certificates by itself (volume `traefik-acme`).
- It forwards the backend paths (`/api/`, `/mcp`, `/healthz`, `/openapi.json`) to the backend,
  `/meta/webhook` to the core, and every other path to the panel (the frontend).
- The gRPC port of the core and PostgreSQL are never published.
- Traefik does not buffer responses, so the pairing stream works through it.
- The proxy must allow at least 6 minutes for an answer on
  `POST /api/v1/instances/{id}/messages`. A send can wait for the pacing of the core, and the
  client SDKs wait for this answer. The repository has no Traefik file for this rule.
- This profile sets `TRUST_PROXY=true`, so the rate limits see the real client address. The
  backend port is not published, so only Traefik can reach the backend. No other process can
  send a forged `X-Forwarded-For` header.
- `CORE_TOKEN` gives full access to the core. Keep it only in `.env`.
- Not verified yet: a run of this setup with a real domain and a real certificate.

</details>

### Meta Cloud webhook

Meta sends the events of a Meta Cloud instance to `https://<CALYX_DOMAIN>/meta/webhook`.

1. Set `META_WEBHOOK_PORT` (for example `8081`) and `CORE_META_VERIFY_TOKEN` in `.env`.
2. Compose gives `CORE_META_VERIFY_TOKEN` to the core as `META_WEBHOOK_VERIFY_TOKEN`, and to the
   backend as `CORE_META_VERIFY_TOKEN`. So the two values are always equal.
3. The owner reads the URL and the token in `GET /api/v1/organization`.
4. The owner copies both values into the Meta app.

## Install without a domain

To run the whole product on one machine with no domain, use the compose profile `local`. It runs
the panel behind a gateway on `127.0.0.1:8080`. See "The profile `local`" in
[apps/frontend/README.md](../frontend/README.md).

The compose profile `backend` is for production behind Traefik only. It publishes no backend
port.

To run only the backend from the source:

1. Start the core yourself.
2. Start a PostgreSQL that keeps its data. This command uses a named volume and publishes the
   port on `127.0.0.1` only:

   ```bash
   docker run -d --name calyx-postgres -e POSTGRES_PASSWORD=<password> \
     -v calyx-pgdata:/var/lib/postgresql/data -p 127.0.0.1:5432:5432 postgres:17-alpine
   ```

3. Set the variables of [Configuration](#configuration). `DATABASE_URL` is
   `postgres://postgres:<password>@127.0.0.1:5432/postgres`. Set `PUBLIC_URL` to
   `http://127.0.0.1:8080`, and keep `TRUST_PROXY` unset (`false`).
4. Run `pnpm --filter @calyx/backend dev`. The backend listens on `127.0.0.1:8080`.

## Send your first message

With the panel:

1. Open the invitation mail, or the link that `org create` printed. Set your password.
2. In the panel, open **Instances** and select **+ Connect a number**. Scan the QR code with the
   phone, or type the pairing code.
3. Open **API keys** in the sidebar and create a key. The panel shows the key once: copy it.
4. Open **Connect a program** in the sidebar. It shows the address of Calyx, the header with
   your key and the packages of the [official SDKs](../../README.md#official-sdks). Each
   instance has its ID on its card.

With `curl`, after step 3:

1. Find the `id` of the instance, on its card in the panel or with this call:

   ```bash
   curl -H "Authorization: Bearer cx_..." "$PUBLIC_URL/api/v1/instances"
   ```

2. Send a message. The `Idempotency-Key` makes a retry safe (see
   [Send messages](#send-messages)):

   ```bash
   curl -X POST "$PUBLIC_URL/api/v1/instances/<id>/messages" \
     -H "Authorization: Bearer cx_..." -H "Content-Type: application/json" \
     -H "Idempotency-Key: $(uuidgen)" \
     -d '{"to":"34600000000","text":"Hello from Calyx"}'
   ```

   The answer is `{"message_id":"...","replayed":false}`.

The full reference of every operation is on the page `/docs` of the panel.

---

# Use the API

## The HTTP API

- The base path is `/api/v1`. The body is JSON. Times are RFC 3339 in UTC. Ids are UUIDs.
- A list is `{"items": [...]}`. There is no pagination.
- Names (display names, client names and similar) refuse control characters. The answer is 400
  `invalid_request`.
- The full description is an OpenAPI 3.1 document, generated from the code:
  - the backend serves it at `GET /openapi.json`;
  - the command `backend openapi` prints it;
  - the repository keeps a copy in `packages/shared-types/openapi/backend.json` for SDK authors;
  - the panel shows it on the page `/docs`.
- Each operation has one tag: "Account", "Organization", "Clients", "Instances", "Messages",
  "Bulk jobs" or "Events".
- Each operation that needs a permission has the extension `x-calyx-roles`. It lists the roles
  (`owner`, `client`) that can call the operation. The backend computes it from
  `tenancy.permissions`. A public operation has no marker. The marker only describes the roles.
  It does not control access.

## Log in and API keys

There are two ways in: the login cookie of the panel, and API keys for your programs.

**The login cookie**

- Log in with `POST /api/v1/auth/login`. It sets the cookie `calyx_session`. The cookie lasts 30
  days after the last use, and 90 days at most.
- A browser request that changes data with the cookie must send an `Origin` header equal to
  `PUBLIC_URL`. Without it, the answer is 403 `csrf_check_failed`.

**API keys**

- Create an API key on the page **API keys** of the panel (`/developers/api-keys`). The panel
  shows a new key once.
- Send it as `Authorization: Bearer cx_...`. A request with an API key needs no `Origin` header.
- Revoke a key on the same page, or with `DELETE /api/v1/api-keys/{id}` (`revokeAPIKey`).

> [!CAUTION]
> After you revoke an API key that another person can have, check the webhooks of every
> instance. A webhook that the API key set keeps working after the revoke.

**Operations for the login cookie only.** Eight operations refuse an API key, with 403
`api_key_not_allowed`. The reason: a leaked API key must not take over the account, create more
keys or change who can log in.

- `PUT /api/v1/me/password` (`changePassword`)
- `POST /api/v1/organization/whatsapp-web-acknowledgement` (`acknowledgeWhatsAppWeb`)
- `POST /api/v1/api-keys` (`createAPIKey`)
- `POST /api/v1/clients/{id}/invitations` (`inviteClientUser`)
- `DELETE /api/v1/clients/{id}/invitations` (`cancelInvitation`)
- `DELETE /api/v1/clients/{id}/user` (`removeClientAccess`)
- `POST /api/v1/auth/logout` (`logout`)
- `GET /api/v1/events` (`followEvents`)

<details>
<summary><b>Without the panel: log in, create an API key and accept the WhatsApp Web text with <code>curl</code></b></summary>

1. Log in and keep the cookie:

   ```bash
   curl -c cookies.txt -X POST "$PUBLIC_URL/api/v1/auth/login" \
     -H "Content-Type: application/json" \
     -d '{"email":"owner@example.test","password":"..."}'
   ```

2. Create an API key with the cookie and the `Origin` header. The answer shows the key once:

   ```bash
   curl -b cookies.txt -X POST "$PUBLIC_URL/api/v1/api-keys" \
     -H "Origin: $PUBLIC_URL" -H "Content-Type: application/json" \
     -d '{"name":"My program"}'
   ```

3. As the owner, accept the WhatsApp Web text:

   ```bash
   curl -b cookies.txt -X POST "$PUBLIC_URL/api/v1/organization/whatsapp-web-acknowledgement" \
     -H "Origin: $PUBLIC_URL" -H "Content-Type: application/json" \
     -d '{"accept":true}'
   ```

</details>

**Rate limits.** Login, password reset, and the acceptance of an invitation or a reset allow 10
requests per minute per IP address and 5 per minute per email. After that, the answer is 429
`rate_limited` with `Retry-After: 60`. The two token lookups (`lookupInvitation` and
`lookupPasswordReset`) share one limit of 10 requests per minute per IP address. They take the
token in the body, so no log line holds it.

## Errors

Errors are `application/problem+json`. Write your code against `code`:

```json
{"type":"https://calyx.dev/errors/<code>","title":"...","status":409,"detail":"...","code":"<code>"}
```

A 400 `invalid_request` for wrong fields also has `errors`, one item per wrong field:

```json
{"type":"https://calyx.dev/errors/invalid_request","title":"Bad Request","status":400,
 "detail":"The request has invalid fields.","code":"invalid_request",
 "errors":[{"location":"body.messages[3].to","message":"is required."},
           {"location":"body.messages[5].text","message":"must have at most 4096 characters."}]}
```

- `location` is the path of the field in the request. `body` alone is the whole body.
- The answer has at most 20 items. A 21st item says how many more there are.
- A message never repeats the value that you sent.
- The backend checks the shape first: types, required fields, unknown fields. A request with a
  shape error gets only its shape errors. The other rules run when the shape is correct.

<details>
<summary><b>All the codes, by status</b></summary>

| Status | Codes |
|---|---|
| 400 | `invalid_request` |
| 401 | `unauthenticated` |
| 403 | `csrf_check_failed`, `api_key_not_allowed`, `owner_only`, `instance_creation_not_allowed`, `whatsapp_web_not_allowed`, `whatsapp_web_not_acknowledged` |
| 404 | `not_found` |
| 409 | `quota_exceeded`, `phone_number_id_in_use`, `pairing_in_progress`, `pairing_not_supported`, `instance_in_error`, `instance_connected`, `instance_not_connected`, `instance_not_ready`, `idempotency_key_conflict`, `send_in_progress`, `idempotency_key_outcome_unknown`, `duplicate_message`, `client_has_instances`, `default_client`, `email_in_use`, `conflict` |
| 410 | `token_invalid` |
| 413 | `too_many_messages` |
| 429 | `rate_limited`, `instance_queue_full` |
| 500 | `internal` |
| 502 | `core_rejected_token` |
| 503 | `core_unavailable` |
| 504 | `send_outcome_unknown`, `bulk_outcome_unknown`, `change_outcome_unknown` |

</details>

## People and permissions

- An owner manages the organization, its clients and every instance.
- A client user manages the instances and the bulk jobs of its own client only.
- Anything outside the scope of the caller answers 404, as if it did not exist.
- `GET /api/v1/me` (`getMe`) gives an owner the whole organization, and a client user only its
  `id` and `name`.
- `create_instance_problem` in that answer tells a client user why it cannot create a WhatsApp
  Web instance now: `whatsapp_web_not_allowed`, `whatsapp_web_not_acknowledged`,
  `instance_creation_not_allowed` or `quota_exceeded`. It is `null` when nothing stops it, and
  always `null` for an owner. The answer of `createInstance` stays the authority.

### The user of a client

A client has at most one active user. The owner invites the user, and the person accepts the
invitation on the page `/invite` of the panel, or with `POST /api/v1/invitations/accept`
(`acceptInvitation`).

- `pending_invitation` of a client shows the invitation that nobody accepted yet, also an expired
  one. It is `null` when there is none.
- `DELETE /api/v1/clients/{id}/invitations` (`cancelInvitation`) deletes the pending invitation,
  also an expired one. The answer is 404 when the client has none.
- `POST /api/v1/invitations/lookup` and `POST /api/v1/auth/password-reset/lookup` show the email
  of a token before the person sets a password. They do not use the token.

**Remove the user.** In the panel, the owner opens the client and selects "Remove the user".
Without the panel:

1. The owner calls `DELETE /api/v1/clients/{id}/user` (`removeClientAccess`).
2. The backend disables the user, ends its login sessions and revokes its API keys.
3. The instances, the bulk jobs and the webhooks of the client do not change.
4. A running pairing continues for 3 minutes at most. It gives the removed user no access to
   Calyx.

The answer is 404 when the client has no active user. The default client has no user, so it
always answers 404.

**Bring the user back.** The owner invites the same email to the same client again. When the
person accepts, the user is enabled again with the new password. Its old login sessions, resets
and API keys stay invalid. A user that the operator disabled can also return in this way. The
owner can also invite another email. Any other existing user of that email gives 409
`email_in_use`.

## Pair a WhatsApp Web instance

The panel does all these steps with **+ Connect a number**. With the API:

1. Send `POST /api/v1/instances` with `"pairing":"qr"` or `"pairing":"code"`, and a `"locale"`
   (for example `"es-ES"`). For a code, add `"phone"` (7 to 15 digits with the country code).
2. The answer comes within 15 seconds, with the first update. If the core is slow, it has
   `"status":"CONNECTING"` and the text "Waiting for the core.".
3. Follow the pairing with `GET /api/v1/instances/{id}/pairing` (Server-Sent Events).

The pairing runs in the backend for up to 3 minutes. It also runs while the browser is in the
background, so you can type the code on the same phone.

- The stream sends the events `update`, `done` and `failed`, and a `: ping` comment every 15
  seconds.
- You can close the stream and open it again at any time. After `done`, the stream answers 404.
- For 5 minutes after `failed`, a new follow gives `failed` first, and then the stream ends.
- A 404 on a new follow means that no pairing runs and that the backend keeps no failure. Read
  the instance with `GET /api/v1/instances/{id}`.
- Cancel a pairing with `DELETE /api/v1/instances/{id}/pairing`. The backend deletes a new
  instance whose pairing fails, expires or is cancelled.

<details>
<summary><b>Re-pair an instance that lost its link</b></summary>

`POST /api/v1/instances/{id}/pair` is the re-pair. It works only for a WhatsApp Web instance in
`NEEDS_AUTH`.

- It sends the full stored configuration, webhooks and their secrets included.
- When the instance has no webhooks in the backend, the re-pair first removes the webhooks of the
  core. So a webhook added in the core directly does not stay.
- A disabled instance answers 409 `conflict`. Enable it first. The core also refuses a re-pair
  of a disabled instance, and every change of an instance while that instance pairs.
- An instance in `ERROR` answers 409 `instance_in_error`. Remove it and create it again. This
  does not lift a ban.
- A Meta Cloud instance recovers with new credentials through `PATCH`.

</details>

<details>
<summary><b>Device identity and locale</b></summary>

> [!IMPORTANT]
> These two values change what the phone shows and what the locale fields say. They do not
> change the risk of a ban.

- `device_identity` is what the phone shows in its list of linked devices. Use one of
  `CHROME_WINDOWS`, `EDGE_WINDOWS`, `FIREFOX_WINDOWS`, `FIREFOX_LINUX` and `SAFARI_MAC_OS`.
  Without it, the core picks one at random. A value outside the list answers 400
  `invalid_request`, and the field error lists the allowed values.
- `locale` is a language and a country, for example `es-ES`: two lower-case letters, a hyphen,
  two upper-case letters. A new WhatsApp Web instance needs it. The instance sends it to WhatsApp
  at each connection.

`POST /api/v1/instances/{id}/pair` also accepts both fields. The backend sends only the values
that you give:

- Without `device_identity`, the core picks a new one, because a re-pair links a new device.
- Without `locale`, the core keeps the stored one. An instance created before the backend stored
  a locale has none: its re-pair answers 400 `invalid_request` on `locale` until you give one.

After the pairing, the instance shows the `device_identity` that the core used and the `locale`.
The backend also copies both values from the core at each state check (every 30 seconds). Meta
Cloud instances have neither: both are `null`.

</details>

## Send messages

`POST /api/v1/instances/{id}/messages` with `{"to","text"}` answers `{"message_id","replayed"}`.
`replayed` is `true` when the core returns the answer of an earlier send with the same
`Idempotency-Key`. In that case, the core sends nothing new.

> [!CAUTION]
> A send can wait up to 5 minutes in the queue of the core, before the answer 504
> `send_outcome_unknown`. Set the HTTP timeout of your client to more than 5 minutes.

> [!CAUTION]
> The answer 504 `send_outcome_unknown` means that the message may or may not have reached
> WhatsApp. Send an `Idempotency-Key` header. After a 504, retry with the same key: the core
> sends the message at most once. Without a key, do not retry automatically.

Use a core of the same Calyx version as the backend. An older core does not know the
idempotency key. See [Backups, upgrades and rollback](#backups-upgrades-and-rollback) for the
upgrade order.

### The Idempotency-Key header

The header is optional on `POST /api/v1/instances/{id}/messages` and on
`POST /api/v1/instances/{id}/bulk-jobs`.

- The key has 1 to 255 printable ASCII characters. A wrong key gives 400 `invalid_request`.
- Make a new key for each new message. Use a UUID.
- Use the same key only to retry the same request after an answer that did not arrive.
- The core keeps a key for 24 hours.
- All callers of one instance share its keys.
- A resend with a known key gets its answer also while the instance is not connected. The answer
  is the stored result, or one of the key answers below.
- A send with a new key needs a connected instance. Without one, the answer is 409
  `instance_not_connected`. Send the same request with the same key when the instance is
  connected again.
- With a key, the core call continues when your client stops waiting.

<details>
<summary><b>The answers about the key, and the other answers of a send</b></summary>

Answers about the key:

- 409 `idempotency_key_conflict`: the key belongs to a different request. Use a new key.
- 409 `send_in_progress`: the core still sends the first request. Wait for the seconds in the
  `Retry-After` header, then retry with the same key.
- 409 `idempotency_key_outcome_unknown`: the core could not learn the result of the first send.
  It never sends it again with this key. To send it again, use a new key after 10 minutes.
- 409 `duplicate_message`: the same text went to the same recipient in the last 10 minutes.

Other answers:

- 409 `instance_not_connected`: nothing was sent. Send the same request again later, with the
  same key if it had one.
- 409 `instance_not_ready`: the core refused the send because the state of the instance changed
  during the call (a race). An instance that is disabled, or that needs to authenticate again,
  gives `instance_not_connected`. The backend checks the status of the instance at once.
- 429 `instance_queue_full`: the queue of the core is full. Wait for the seconds in the
  `Retry-After` header, then retry.

</details>

## Bulk jobs

A bulk job sends to many recipients with the pacing of the core. Create it with the API, an SDK
or the MCP server. The panel shows the jobs of an instance and stops a job that waits or sends:
on the card of the instance, select "View" in the row "Bulk jobs". The panel does not create
jobs.

Create a job with `POST /api/v1/instances/{id}/bulk-jobs`, in one of two forms:

- `{"recipients":[...],"text":"..."}`
- `{"messages":[{"to","text","delay_ms"}]}`

Rules:

- A job has at most 5,000 messages. More messages give 413 `too_many_messages`.
- A job cannot send many messages to one person, because that looks like spam. With one `text`,
  each recipient appears once. With `messages`, a recipient gets at most two messages.
- A job above that limit gets 400 `invalid_request`, with an item in `errors` for each message
  above the limit. The core also counts two forms of one number (for example with and without
  `+`) as one recipient.
- The optional field `pacing` has `delay` and `pause_duration` in milliseconds, and
  `pause_every` in messages. Each one is `{"min","max"}`. The core refuses values under its
  minimums (400). The backend never makes the pacing shorter.

Other operations:

- Read a job with `GET /api/v1/bulk-jobs/{id}`.
- Cancel it with `POST /api/v1/bulk-jobs/{id}/cancel` (the button "Stop sending" in the panel).
- List the jobs of an instance with `GET /api/v1/instances/{id}/bulk-jobs`.

The create answer has `replayed`. It is `true` when the core returns an earlier job for the same
`Idempotency-Key`. The status is 201 in both cases. The other bulk answers always give `false`.

> [!CAUTION]
> If the answer to a creation is lost (504 `bulk_outcome_unknown`), a job can run in the core
> without a record in the backend. With an `Idempotency-Key`, retry with the same key: the retry
> gives the job and stores it. Without a key, the backend cannot show or cancel the job, but the
> `bulk.finished` webhook of the core still reports it. Do not start the job again automatically.

## Webhooks of instances

A webhook sends the events of an instance to your URL. In the panel, select "Edit" in the row
"Webhooks" of the instance card. "Edit" shows only for an active instance that is not
send-only. The page sets one URL for all events, or one URL for each event. Without the panel,
use `PATCH /api/v1/instances/{id}`.

- An instance has at most one webhook per event. A send-only instance has none.
- Events for every instance: `connection.update`, `message.received`, `message.status`,
  `bulk.finished`.
- Meta Cloud instances also have `meta.webhook`. WhatsApp Web instances also have the raw events
  `whatsmeow.Message`, `whatsmeow.Receipt` and others. The list is in
  `apps/core/internal/events/types.go`.
- The backend encrypts the secrets with `SECRET_KEY`. It never shows them again (`has_secret`).
- In a `PATCH`, an item without `secret` keeps the stored secret only when the event and the URL
  do not change. With another URL, the webhook has no secret. `"secret": ""` removes it.
- A save of webhooks does not reconnect the instance. The backend sends `enabled` to the core
  only when the request has the field `enabled`.

> [!CAUTION]
> v1 does not filter webhook URLs. The core calls any address that it can reach, private network
> addresses included. Give instance rights only to users that you trust.

## The live state and the health check

The backend subscribes to the `connection.update` events of the core. It gets no message
content. When an event arrives, the backend asks the core for the status of that instance and
saves it. A comparison every 30 seconds also finds a change that no event showed, for example
after a restart.

The field `status_stale` is `true` while the core is `unreachable`. It becomes `false` only when
`core` is `ok` again, so an old state never looks current.

<details>
<summary><b>The event stream of the panel (<code>GET /api/v1/events</code>)</b></summary>

`GET /api/v1/events` (`followEvents`) tells a browser that something changed. Only a login
cookie works. An API key gets 403 `api_key_not_allowed`. The operation is not an MCP tool.

1. The stream sends the event `changed` at its start, and after each change that the user may
   see. The data is `{}`.
2. After `changed`, read the screen again: `GET /api/v1/me`, the instances and `/healthz`.
3. The stream sends a `: ping` comment every 15 seconds.
4. A user can have 10 streams. When the 11th opens, the oldest one gets the event `replaced` and
   closes. After `replaced`, do not connect again at once. A tab that connects again would close
   the stream of the next tab, with no end.

At each ping, the backend checks the login again. The stream ends within 15 seconds after a
logout, a password change, a disabled user or a removed client. The stream of a pairing does the
same check. A database error does not end a stream. Each write has a limit of 10 seconds.

</details>

### Health

`GET /healthz` answers `{"database","core","mail","version","core_version"}`.

| Field | Values | Meaning |
|---|---|---|
| `database` | `ok`, `unavailable` | The answer is 503 only when the database fails. Then `core` is `unknown`. |
| `core` | `ok`, `unreachable`, `unknown` | `ok` needs the event subscription and one successful comparison. |
| `mail` | `configured`, `off` | `configured` says that `SMTP_HOST` is set. It does not say that the mail server works. |
| `version` | text | The version of the backend: the product version in a release image, `dev` in a build from the source. Both answers (200 and 503) have it. |
| `core_version` | text | The version that the core reported in the header `calyx-version` of its event stream. Empty before the first link, or when the core is older. |

Start the backend binary with `--version` to print the version and exit. This command needs no
configuration.

## MCP server for AI agents

The backend has an MCP server at `POST /mcp` (Streamable HTTP). AI agents use it to work with
Calyx. Set `MCP_ENABLED=false` to turn it off.

Access:

- The server accepts only an API key: `Authorization: Bearer cx_...`.
- A session cookie gets 401, also a valid one. Make the API key on the page "API keys" of the
  panel, or as shown in [Log in and API keys](#log-in-and-api-keys).
- Each tool call runs as the user of the API key. A client user sees only the instances of its
  own client.

The six tools:

| Tool | Use |
|---|---|
| `list_instances` | List the instances that you can use, with their status. |
| `get_instance` | Get one instance and its status. |
| `send_message` | Send one text to one recipient. Arguments: `id`, `to`, `text` and `Idempotency-Key`. |
| `create_bulk_job` | Send to many recipients with the pacing of the core. |
| `get_bulk_job` | Get the state and the counts of a bulk job. |
| `cancel_bulk_job` | Stop a bulk job. Messages that the core already sent stay sent. |

Send an `Idempotency-Key` with `send_message` and `create_bulk_job`. Reuse the key only to retry
the same call. The MCP body limit is 1 MiB. Send a large bulk job over HTTP.

**Connect n8n:**

1. Add an MCP Client node (or an AI Agent with an MCP Client tool).
2. Set the endpoint URL to `PUBLIC_URL/mcp`.
3. Set the authentication to Bearer, and paste the API key.
4. Run the node. It lists the six tools.

> [!WARNING]
> An API key keeps every permission of its user. A program with the key can do all that the user
> can do, on every instance that the user can see. Make one key for each program, so you can
> revoke one key and the other keys keep working. A prompt injection can make an agent start a
> bulk job. The pacing of the core and the repeat guard still apply. Give an API key only to
> agents that you trust.

> [!WARNING]
> Tool results contain full resources, such as webhook URLs, `device_identity` and
> `phone_number_id`. They go to the vendor of the language model. A webhook URL can be a secret,
> so decide which agents get an API key.

<details>
<summary><b>Find a tool call in the logs</b></summary>

1. Find the line of the `POST /mcp` request. Read its `request_id`.
2. The `/mcp` line says status 200, also when the tool failed.
3. Search for the log line with `mcp_request_id` equal to that `request_id`.
4. Read the route and the status on that line. The route shows the tool. The status is the real
   result.

</details>

---

# Run and maintain

## Configuration

The backend reads environment variables. In compose, it gets most of these values from `.env`.

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `DATABASE_URL` | yes | none | PostgreSQL URL (`postgres://...`). |
| `PUBLIC_URL` | yes | none | Origin of the API, such as `https://api.your-domain.test`. Used in mail links and the CSRF check. |
| `CORE_ADDRESS` | yes | none | `host:port` of the gRPC port of the core. |
| `CORE_TOKEN` | yes | none | At least 32 characters. The token that the core expects (`GRPC_AUTH_TOKEN` of the core). |
| `SECRET_KEY` | yes | none | 32 bytes in standard base64. Encrypts the webhook secrets. |
| `HTTP_HOST` | no | `127.0.0.1` (`0.0.0.0` in the image) | Address the server listens on. |
| `HTTP_PORT` | no | `8080` | Port the server listens on (1 to 65535). |
| `CORE_META_VERIFY_TOKEN` | no | empty | Verify token of the Meta webhook. The backend sends it to the core with the URL `PUBLIC_URL/meta/webhook`. |
| `SMTP_HOST` | no | empty | SMTP server. Empty means no mail. Set it with `SMTP_FROM` and `OPERATOR_EMAIL`. |
| `SMTP_PORT` | no | `587` | SMTP port. Port 465 is not supported (see [Mail](#mail)). |
| `SMTP_USERNAME` | no | empty | SMTP user. Needs `SMTP_PASSWORD`. |
| `SMTP_PASSWORD` | no | empty | SMTP password. |
| `SMTP_FROM` | with `SMTP_HOST` | empty | Sender address. |
| `OPERATOR_EMAIL` | with `SMTP_HOST` | empty | Address that gets the "core unreachable" mail. |
| `WHATSAPP_WEB_DEFAULT` | no | `allowed` | `allowed` or `blocked`. Default for new organizations. |
| `TRUST_PROXY` | no | `false` | `true` makes the backend read the client address from `X-Forwarded-For`. Set it only behind a proxy that you control. |
| `MCP_ENABLED` | no | `true` | `false` turns off the MCP server: `/mcp` then answers 404. |
| `LOG_FORMAT` | no | `text` (`json` in the image) | `text` or `json`. |

Compose also reads `CALYX_DOMAIN`, `ACME_EMAIL`, `POSTGRES_PASSWORD` and `META_WEBHOOK_PORT`. The
backend does not read them.

<details>
<summary><b>How the backend checks the configuration, and the fixed values</b></summary>

At start, every command checks the whole configuration. When a value is wrong or missing, the
backend prints one line for each problem and stops with status 1. It also refuses:

- placeholder values from `.env.example`: hosts and emails under `example.com`, `example.org` and
  `example.net`, and the values `changeme`, `change-me` and `replace-me`;
- a `CORE_TOKEN` shorter than 32 characters;
- a `PUBLIC_URL` with `http` when the host is not `localhost`, `127.0.0.1` or `::1`.

When SMTP is configured, `serve` also connects to the SMTP server at start (EHLO, STARTTLS and
AUTH, no mail sent, 10 s limit). When this check fails, the backend logs an ERROR that says the
alert mails will fail, and it keeps running. A mail server outage must not stop the API.

Durations are not variables: the state check runs every 30 s, the alert waits 5 min, and a
pairing lasts 3 min. The core name is `main`.

Traefik routes the path `/meta/webhook` of the same domain to the core, so the backend derives
the Meta webhook URL from `PUBLIC_URL`. It sends the URL only when `CORE_META_VERIFY_TOKEN` is
set.

</details>

## Operator commands

The server admin runs these commands in the backend container:

```bash
docker compose --profile backend exec backend backend <command>
```

Or run the binary with the same environment as the server.

```text
Usage: backend <command> [flags]

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
  help                  Show this text.

Every command reads the same environment variables as the server, except openapi, help and --version.
```

Exit codes: `0` success, `1` error, `2` wrong usage.

- `org set-owner` invites a new owner. The old owner stays until the new one accepts. Then the
  backend disables the old owner. A move of the ownership to an existing client user is not
  supported.
- `instances orphans` lists the instances of the core that the backend does not know. The backend
  does not adopt them. Create them again through the API.
- `user disable` deletes the sessions of the user and revokes the API keys of the user.

<details>
<summary><b>Examples: <code>org create</code>, <code>user reset-password</code> and <code>instances orphans</code></b></summary>

`org create` without mail (the command prints the link):

```text
$ backend org create --name "My company" --owner-email you@example.com
Organization created.
id: <uuid>
name: My company
max instances: 10
WhatsApp Web: allowed
Owner invitation for you@example.com, valid until <time>.
The invitation mail was not sent. Give this link to the owner:
https://<domain>/invite?token=<token>
Without a frontend, run (see the caution after the next example):
curl -X POST https://<domain>/api/v1/invitations/accept -H 'Content-Type: application/json' -d '{"token":"<token>","password":"<new password>"}'
```

With mail, the output has the line `The invitation mail was sent.` and no link.

`user reset-password`:

```text
$ backend user reset-password --email you@example.com
Reset link for you@example.com (valid for 1 hour):
https://<domain>/reset-password?token=<token>
Without a frontend, run:
curl -X POST https://<domain>/api/v1/auth/password-reset/confirm -H 'Content-Type: application/json' -d '{"token":"<token>","password":"<new password>"}'
```

The link opens the panel. Without the panel, replace `<new password>` in the `curl` command and
run it.

> [!CAUTION]
> A password typed in the `curl` command stays in the shell history, and `ps` shows it to the
> other users of the machine. Use the safer form below.

1. Read the password into a variable. The terminal does not show it:

   ```bash
   read -s -p "New password: " NEW_PASSWORD; echo
   ```

2. Write the JSON body to a file that only you can read, and send the file:

   ```bash
   umask 077
   printf '{"token":"<token>","password":"%s"}' "$NEW_PASSWORD" > body.json
   curl -X POST https://<domain>/api/v1/auth/password-reset/confirm \
     -H 'Content-Type: application/json' --data @body.json
   rm body.json
   ```

   For an invitation, use `/api/v1/invitations/accept`. A password with `"` or `\` needs JSON
   escapes, so use a password without these characters.

The backend serves no page. When no panel is installed, `GET` on `/invite`, `/reset-password` and
`/instances/{id}` answers 404 in plain text. The text names the operation of the API to use.

`instances orphans` when the core has no unknown instance:

```text
$ backend instances orphans
No orphan instances.
```

</details>

## Alerts

The owner of the organization gets one mail when an instance:

- goes to `NEEDS_AUTH` or `ERROR`;
- stays `DISCONNECTED` longer than 5 minutes;
- disappears from the core.

The server admin (`OPERATOR_EMAIL`) gets one mail when two checks in a row cannot reach the core.
Then the instance answers have `"status_stale": true`, and `/healthz` says
`"core":"unreachable"`.

An alert mail links to the instance in the panel (`PUBLIC_URL/instances/{id}`). Its last line
gives the operation of the API for the same step, with the API address of the instance.

<details>
<summary><b>When an alert goes out, and its limits</b></summary>

- The backend compares the states of the core every 30 seconds, and after each
  `connection.update` event of the core (see
  [The live state and the health check](#the-live-state-and-the-health-check)).
- The mail for `NEEDS_AUTH` goes out at once, because an event starts a check. The mail for
  `DISCONNECTED` waits 5 minutes. The mail for `ERROR` and for a missing instance goes out after
  the check that finds it.
- A mail goes out only after a comparison that succeeded, so no mail tells a state that nobody
  confirmed.
- The backend tries a failed mail again with a growing wait, up to one hour between attempts.
- A restart of the backend delays an alert by at most one comparison. It does not lose the
  alert.

Limits:

- A sleeping send-only instance, or an idle Meta Cloud instance with an expired token, raises an
  alert only when it next sends.
- Alerts go to the owner only.

</details>

## Mail

- SMTP with STARTTLS when the server offers it. Port 465 (TLS from the first byte) is not
  supported. Use port 587. The password goes out only after STARTTLS, because the Go library
  refuses a password on a plain connection to a remote server.
- Check the SMTP settings with `backend mail test <address>`. It sends one short mail and prints
  `sent` or the SMTP error.
- The backend sends each mail at once, with a limit of 15 seconds. It does not queue mails.
- The backend sends the mail of a password reset in the background, so the answer does not show
  whether the email exists.
- Without mail (`SMTP_HOST` empty): the API answer to the owner who invited has the invitation
  link, or the operator command prints it. A self-service reset request then does nothing
  visible. The operator resets the password with `user reset-password`.
- An invitation never says whether the email has an account. The check happens at acceptance, and
  only the holder of the link sees it. Without mail, the inviter holds the link, so the inviter
  can learn at acceptance that an email has an account.

## Backups, upgrades and rollback

Back up three things:

1. The database:

   ```bash
   docker compose --profile backend exec postgres pg_dump -U calyx calyx > calyx-backup.sql
   ```

2. `SECRET_KEY`, with the database. Without it, the backend cannot read the stored webhook
   secrets. The only fix is to enter every secret again.
3. The volume `core-data` of the core. It holds the WhatsApp Web sessions.

To upgrade:

1. Take a backup.
2. Upgrade the core.
3. Upgrade the backend.

The order matters: an older core answers `UNIMPLEMENTED` to `GetBulkJobs`, and then
`listBulkJobs` answers 500.

Migrations in v1 only add. A backend refuses to start on a database that a newer backend
migrated. To roll back, restore the backup and start the older image.

## What the database holds

The database holds:

- Users, organizations and clients.
- Instance records and their last status.
- Webhook URLs, with encrypted secrets.
- The owner of each bulk job.
- Hashed tokens.

The database never holds:

- Message text and recipients.
- Meta access tokens and Meta app secrets. The core holds them.
- Plain passwords (only argon2id hashes).
- Plain session, API key, invitation or reset tokens.
- Plain webhook secrets.

The logs hold none of these values either.

---

# Development

> [!CAUTION]
> Name the service. Without a name, compose starts every service of the active profiles.

1. Start the test database:

   ```bash
   docker compose -f docker-compose.dev.yml --profile test up -d postgres-test
   ```

2. Run the tests:

   ```bash
   TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/postgres pnpm --filter @calyx/backend test
   ```

   Without `TEST_DATABASE_URL`, the database tests fail and print the command to run.
   `go test -short` skips them.
3. After a change to a query or a migration, run `pnpm --filter @calyx/backend generate`.
   `pnpm --filter @calyx/backend generate:check` exits with 1 when `internal/database/gen` is out
   of date. Never edit `internal/database/gen` by hand.
4. Run the end-to-end test. It needs a C compiler for the core:

   ```bash
   cd apps/backend && TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/postgres go test -tags e2e -count=1 -timeout 10m ./internal/e2e/
   ```

5. After a change to an operation of the HTTP API, run
   `pnpm --filter @calyx/backend openapi:generate`. It writes the OpenAPI document to
   `packages/shared-types/openapi/backend.json`. `pnpm --filter @calyx/backend openapi:check`
   exits with 1 when the copy is out of date, and a test fails too.
6. Run `pnpm --filter @calyx/backend vet` and `gofmt -l .`.

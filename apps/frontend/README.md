# Calyx frontend

The web page of Calyx. It is a static single page (React, Vite) on top of the backend. It needs the
backend: it keeps no data of its own.

```text
browser ──▶ gateway / proxy ──▶ backend   (/api/, /mcp, /healthz, /openapi.json)
                            └─▶ frontend  (the page: static files, nginx)
```

## The pages

| Path | What it shows |
|---|---|
| `/` | The instances, and the drawer to connect a new one (`/instances/new`). |
| `/instances/:id` | One instance. |
| `/instances/:id/webhooks` | The webhooks of the instance: one URL for all events, or one URL for each event. |
| `/instances/:id/bulk-jobs` | The bulk jobs of the instance, and the button "Stop sending". The page does not create jobs. |
| `/clients/:id` | One client: its access ("Remove the user"), its limits and its deletion. |
| `/developers/api-keys` | The API keys: create a key (shown once) and revoke a key. |
| `/developers/connect` | How to connect a program to Calyx. |
| `/account` | The account of the person. |
| `/organization` | The organization. Only an owner sees it. |
| `/organization/whatsapp-web` | The WhatsApp Web terms. Only an owner sees it. |
| `/docs` | The API reference, drawn by Scalar from `/openapi.json`. It needs a login, and shows only the operations that the role of the person can call. `/openapi.json` stays public. |

The paths `/login`, `/forgot-password`, `/reset-password` and `/invite` are for a person with no login.

The sidebar is the navigation. From `sm`, it is a fixed column on the left side (an `<aside>`), with no top bar. It holds
the logo, the entries, the health status ("Details" opens the card of services), the version of Calyx and the theme
button. At the phone width, a small bar shows the button "Menu" and the logo. The button opens the same sidebar in a
sheet. The live region of the health status is in the layout, so it works with the sheet closed.

The sidebar lists the pages in four groups: "Instances", "Developers", "Organization" and "Account". The group
"Organization" is only for an owner. A client user gets the "Not found" page on those paths. The API answers 404 to that
user too. The entry "API docs" opens `/docs`, which is a separate page. The file `src/app/navigation.ts` holds
the groups and the rule for the active entry.

Notes on three screens:

- API keys: the sentence of the section links "Connect a program". The button "Create API key" is at the end of the
  section and opens the form in place.
- Organization: it shows "Instances: N of M" and says that only the server admin can change the limit. The popover "How do
  I change the limit?" has the tabs Docker and Manual (the name of the group is "How do you run Calyx?") and shows one command.
- The people: the panel and `/docs` say "server admin" (the operator), "organization admin" (the owner) and
  "client user". The backend, the API, the SDKs and the READMEs keep "operator" and "owner".
- The phone width: the bar shows the logo first and "Menu" at the right. "Menu" opens the sidebar in a Sheet from the right.
- `/docs`: a link "Back to Calyx" and a button "Go to the API reference" (it scrolls to the reference) sit in the header and at the top. The section "Ways to connect to Calyx" lists the official SDKs, each with its language, its package and what
  its client covers.

The frontend has two HTML pages, and each page has its own entry, its own Content-Security-Policy (CSP) and its own size
limit:

| Page | Entry | CSP file | Limit of JavaScript (gzip) |
|---|---|---|---|
| The panel (every path except `/docs`) | `index.html` | `headers.conf` | 250,000 bytes. The panel has no dynamic import. |
| The API reference (`/docs`) | `docs/index.html` | `headers-docs.conf` | 1,500,000 bytes. This is the ceiling. |

The page of `/docs` is large, because Scalar and its dependencies go in this page only. The build checks each limit and
fails above it. The limit of `/docs` follows this rule: the size of the build plus 10 %, rounded up to the next
10,000 bytes, and never above the ceiling. The size is 1,457,682 bytes now, so the limit is the ceiling.

## Development

Run all commands from the repository root.

Caution: set `PUBLIC_URL=http://localhost:5173` on the backend. The backend accepts a change
only from the origin in `PUBLIC_URL`, and Vite keeps the origin of the page. With another value,
each change (also the login) gets 403 `csrf_check_failed`.

1. Start a backend that listens on `127.0.0.1:8080`, with `PUBLIC_URL=http://localhost:5173`.
   See "Local use without a domain" in `apps/backend/README.md`, or use `docker-compose.dev.yml`
   (it sets this value).
2. Run `pnpm --filter @calyx/frontend dev`.
3. Open `http://localhost:5173`. Vite sends `/api`, `/healthz` and `/openapi.json` to the backend.
   To use another backend address, set `CALYX_API`.

Other commands:

| Command | What it does |
|---|---|
| `pnpm --filter @calyx/frontend build` | Writes `dist/` and `dist/version.json`. Fails above the size limits. |
| `pnpm --filter @calyx/frontend lint` | Biome. |
| `pnpm --filter @calyx/frontend typecheck` | TypeScript. |
| `pnpm --filter @calyx/frontend generate` | Makes the types of the API from `packages/shared-types/openapi/backend.json`. Never edit `src/api/generated`. |

## The tests

| Command | What it tests |
|---|---|
| `pnpm --filter @calyx/frontend test` | Unit tests (vitest) of the modules with rules. |
| `pnpm --filter @calyx/frontend test:ui` | Screens, in a browser (Playwright). |
| `pnpm --filter @calyx/frontend test:ui:csp` | Each component against the Content-Security-Policy. |
| `pnpm --filter @calyx/frontend test:image` | The built image: the rules of nginx and the gateway. Needs Docker. |

No test uses real WhatsApp, a compose project or a `.env` file.

### Run the browser tests

The browser tests need the test database. Their test program is
`apps/backend/internal/e2e/uiserver_test.go`: the real backend binary with a fake core and a
fake SMTP server.

1. Start the test database: `docker compose -f docker-compose.dev.yml --profile test up -d postgres-test`.
2. Run the tests:
   `TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/postgres pnpm --filter @calyx/frontend test:ui`.

Playwright runs 6 workers. Each worker has its own lane: a test program, a database and a
`vite preview` of the one build. A test that stops the core or the backend changes only its own
lane. All the tests of one spec file run on one worker, in their order.

To run the test program by hand, start `vite preview --port 4180` with `CALYX_API=http://127.0.0.1:18080`. The test program expects the panel at `localhost:4180`.

| Variable | Default | What it does |
|---|---|---|
| `CALYX_UI_WORKERS` | `6` | The number of workers and lanes, from 1 to 10. `1` runs one spec file after the other. |
| `CALYX_UI_PORT_BASE` | `18080` | Moves all the ports by the same offset. To run two suites at the same time, use two checkouts (the suites share `dist/` and `dist-csp/`) with bases at least 100 apart, for example `28080`. |

The ports of lane `i`, with the default base:

| Part | Port |
|---|---|
| The backend | `18080 + i` |
| The control endpoint of the test program | `18100 + i` |
| The `vite preview` of the lane | `4180 + i` |
| The preview of the CSP test (no lane) | `4174` |

Each test program makes its own database (`calyx_test_<random>`) and drops it when it stops.
An interrupted run can leave one such database for each lane in `postgres-test`. The project
has no command for them: drop them by hand (`DROP DATABASE <name> WITH (FORCE)`).

## The paths and the headers (for another proxy)

The page and the backend share one origin. A proxy in front of them must send each path to the
right service.

| Path | Goes to |
|---|---|
| `/api/`, `/mcp`, `/healthz`, `/openapi.json` | The backend (port 8080) |
| `/meta/webhook` | The core (needs `META_WEBHOOK_PORT`; see `apps/backend/README.md`) |
| `/docs` and everything else | The frontend image (port 8080) |

Rules for the proxy:

1. Give the core path and the backend paths a higher priority than the frontend.
2. Overwrite `X-Forwarded-For` with the address that the proxy saw. Do not append to it. The
   backend runs with `TRUST_PROXY=true` and uses this header for its rate limits.
3. Do not buffer the responses of the backend. The pairing and the live updates are streams.
4. Allow 370 seconds for an answer, and a body of 17 MiB. A send can wait 6 minutes.
5. Do not remove or change the security headers of the frontend: `Content-Security-Policy`,
   `X-Content-Type-Options`, `Referrer-Policy` and `Permissions-Policy`. The file `headers.conf`
   is their source for the panel, and `headers-docs.conf` is their source for `/docs`.
6. Do not write an access log with the full address. An address can hold a token.

The image answers 503 (`text/plain`) on each backend path and on the core path, so a stopped
backend never gives a page with the status 200. `/docs` gets the docs page. Any other path gets
`index.html`.

Example for nginx (inside a `server` block that has the TLS settings):

```nginx
resolver 127.0.0.11 valid=5s;   # the DNS of Docker; use your own resolver

location ~ ^/(api(/|$)|mcp(/|$)|healthz(/|$)|openapi\.json$) {
    set $backend http://backend:8080;
    proxy_pass $backend;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header Connection "";
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 370s;
    proxy_send_timeout 370s;
    client_max_body_size 17m;
}

location ~ ^/meta/webhook(/|$) {
    set $core http://core:8081;   # your META_WEBHOOK_PORT
    proxy_pass $core;
    proxy_set_header X-Forwarded-For $remote_addr;
}

location / {
    set $frontend http://frontend:8080;
    proxy_pass $frontend;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-For $remote_addr;
}

access_log off;
```

Example for Caddy (`Caddyfile`). Caddy does not buffer by default. `header_up` overwrites the header:

```caddyfile
calyx.your-domain.test {
    @backend path /api /api/* /mcp /mcp/* /healthz /healthz/* /openapi.json
    handle @backend {
        request_body {
            max_size 17MB
        }
        reverse_proxy backend:8080 {
            flush_interval -1
            header_up X-Forwarded-For {remote_host}
            transport http {
                read_timeout 370s
                write_timeout 370s
            }
        }
    }
    @core path /meta/webhook /meta/webhook/*
    handle @core {
        reverse_proxy core:8081 {
            header_up X-Forwarded-For {remote_host}
        }
    }
    handle {
        reverse_proxy frontend:8080 {
            header_up X-Forwarded-For {remote_host}
        }
    }
}
```

The labels of `docker-compose.yml` for the profile `backend` set these rules in Traefik. The core
path has priority 20, the backend paths have priority 10, and the frontend has priority 1 (the host
only). If the backend container stops, its requests reach the frontend, which answers 503.

## The profile `local`: use without a domain

The profile `local` runs the whole product on one machine, with no domain and no HTTPS. It has a
`gateway` (nginx) on `127.0.0.1:8080`. Open `http://localhost:8080`.

Meta callbacks do not work here. Meta cannot reach `127.0.0.1`, so `/meta/webhook` answers 503.
Use the profile `backend` with a domain for Meta Cloud instances.

The profile uses the port 8080, like `docker-compose.dev.yml`, so the two do not run together.
All local users share one address for the rate limits, because the gateway sees one address.

1. Copy `.env.example` to `.env`.
2. Set these values in `.env`:

   | Variable | Value |
   |---|---|
   | `POSTGRES_PASSWORD` | Letters and digits only. Make one: `openssl rand -hex 24` |
   | `CORE_TOKEN` | 32 characters at least. Make one: `openssl rand -hex 32` |
   | `SECRET_KEY` | 32 bytes in base64. Make one: `openssl rand -base64 32` |
   | `PUBLIC_URL` | `http://localhost:8080` |

   `CALYX_DOMAIN` and `ACME_EMAIL` are only for the profile `backend`.
3. Start the services. Always name them: without names, compose starts every service of the
   profile.

   ```bash
   docker compose --profile local up -d --build gateway frontend postgres core backend
   ```

4. Create the first organization. The command prints the invitation link when no mail is set:

   ```bash
   docker compose --profile local exec backend backend org create --name "My company" --owner-email you@example.com
   ```

5. Open the link, or open `http://localhost:8080` and log in.

To stop the services, run `docker compose --profile local stop gateway frontend backend core postgres`.
The gateway starts even when the backend is stopped. Then a backend path gives 502, and the page
opens.

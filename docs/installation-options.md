# Installation options

Calyx has layers. Install only the layers you need: each one works without the layers above it.

Caution: always name the services in a `docker compose` command. Without names, compose starts
every service of the active profiles.

## 1. Core alone

- Start: `docker compose --profile core up -d core`.
- You get: the gRPC API of the core, WhatsApp Web and Meta Cloud instances, the pacing, webhooks
  delivered by the core, and the event stream.
- You do not get: users or tenants. One token (`GRPC_AUTH_TOKEN`) gives access to everything.
- For: developers who write their own backend or use the core from one program.

## 2. Core and Redis

- Start: set `REDIS_URL=redis://cache:6379` in `.env`, then
  `docker compose --profile core --profile core-redis up -d core core-worker cache`.
- You get: everything of option 1. Webhooks go through a Redis queue, and a separate worker
  delivers them with retries.
- For: integrations that must not lose webhook deliveries when a receiver is down for a while.

## 3. Core, backend, frontend, PostgreSQL and Traefik

- Start: see `apps/backend/README.md`, "Install with a domain (production)".
- You get: everything of option 1, plus an HTTP API with users and API keys, organizations and
  their clients with isolation between them, quotas per client, mail alerts when an instance
  disconnects, bulk jobs owned per tenant, a web page (the frontend), HTTPS certificates from Let's Encrypt, and operator
  commands.
- For: a company, or a company that manages WhatsApp numbers for its own clients.

| | Core | Core + Redis | Core + backend + frontend + PostgreSQL + Traefik |
|---|---|---|---|
| WhatsApp Web and Meta Cloud instances, pacing | yes | yes | yes |
| Webhooks with retries through a queue | no | yes | with Redis on the core |
| Users, logins, API keys | no (one token) | no (one token) | yes |
| Organizations, clients, isolation, quotas | no | no | yes |
| Mail alerts when an instance disconnects | no | no | yes |
| HTTPS certificates | your own setup | your own setup | Traefik, automatic |
| Web UI | no | no | yes (the frontend) |

Using WhatsApp Web is against WhatsApp's Terms of Service, and WhatsApp can ban the number. No
option removes this risk.

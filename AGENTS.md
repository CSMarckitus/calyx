# Calyx API: instructions for coding agents

Self-hosted integration engine for WhatsApp. The core connects each number ("instance") through
WhatsApp Web (unofficial, with [whatsmeow](https://github.com/tulir/whatsmeow)) or through the
official Meta WhatsApp Business Cloud API. Guardrails against spam live in the core.

Read `README.md` and `CONTRIBUTING.md` first.

## Safety rules

- **Never read, print or copy the `.env` files.** They hold the secrets (`CORE_TOKEN`,
  `SECRET_KEY`, `SMTP_PASSWORD`, `POSTGRES_PASSWORD`). Use `.env.example` to learn the variables.
  The settings in `.claude/settings.json` and `.codex/config.toml` block the read.
- **Never read the session database of the core** (`/data`). It holds the WhatsApp credentials.
- **Do not send real WhatsApp messages, and do not pair or unpair instances,** unless the person
  asks for it at that moment. A careless test can get a number banned.
- **Never commit `.env` files or session databases.** Check `git status` before a commit.
- **Never weaken the guardrails.** Do not add an option that removes, lowers or skips the pacing of
  the core. Do not help to send bulk unsolicited messages.

## How to work

- Write tests first. Go tests always run with `-race`.
- Keep changes small and readable. No speculative abstractions. Ask before you add a dependency.
- Comments say why, not what, and stay short. Write a comment only where the reason is not
  obvious.
- Everything in the repository is English: code, comments, docs, commit messages, log and error
  messages.
- Changes to the `.proto` files, to the HTTP operations of the backend or to
  `packages/client-sdk/CONTRACT.md` are API changes. Discuss them in an issue first.

## Where the truth is

| You need | Read |
|---|---|
| How a component works | its README: `apps/core`, `apps/backend`, `apps/frontend`, `packages/client-sdk/{typescript,go,php/client,php/core,python/backend,python/core,vb6}` |
| The rules that every client SDK follows | `packages/client-sdk/CONTRACT.md` |
| The API of the core | `packages/shared-types/proto/calyx/core/v1/*.proto` |
| The API of the backend | `packages/shared-types/openapi/backend.json` |
| The layers and the folders | `docs/architecture.md` |
| The installation options | `docs/installation-options.md` |

## Architecture: three layers

```
frontend ──HTTP──▶ backend ──gRPC──▶ core
client-sdk ──HTTP──▶ backend
client-sdk ──gRPC──▶ core   (core client, private network)
```

- The core knows nothing about users, tenants or tokens. It sits on a private network.
- Dependencies point downwards only: frontend → backend → core. The core never calls the backend.
- Redis is optional for the core. Without `REDIS_URL`, the core delivers webhooks directly.
- The frontend never calls the core, and it makes no request to another origin.

## Writing style

All text follows ASD-STE100 (Simplified Technical English):

- One topic per sentence. Procedural sentences: 20 words maximum. Descriptive sentences: 25 words
  maximum.
- Write procedures as numbered steps, one instruction per step, with the verb in the imperative.
- Use the active voice and simple verb tenses.
- Use one word for one meaning, and always the same term for the same thing (for example
  "instance", never "account" or "session" for the same thing).
- Put warnings and cautions before the step they apply to, and give the reason.

## Commands

Run from the repository root. Use pnpm only: `pnpm --filter <package> <script>`. Each
`package.json` lists its scripts.

> [!CAUTION]
> Always name the services in a `docker compose` command. Without names, compose starts every
> service of the active profiles.

```bash
docker compose -f docker-compose.dev.yml --profile test up -d postgres-test   # test database (port 55432)
TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/postgres pnpm --filter @calyx/backend test
```

- Go code must pass `lint` with 0 issues.
- PHP and Composer run only in Docker, through `packages/client-sdk/php/docker/run.sh`.
- Python, pip and uv run only in Docker, through `packages/client-sdk/python/docker/run.sh`.
- The product version comes from the `version` field of the root `package.json`. See "Change the
  product version" in `CONTRIBUTING.md`.

## Generated code

Never edit generated code. Change the source (`.proto`, query, migration, operation, generator)
and run the `generate` script of the package. The generated folders include:

- `packages/shared-types/go/corev1`
- `apps/backend/internal/database/gen`
- `packages/shared-types/openapi/backend.json`
- `apps/frontend/src/api/generated`
- `packages/client-sdk/typescript/src/generated`
- `packages/client-sdk/go/backend/types_gen.go`
- `packages/client-sdk/php/client/src/Generated` and `packages/client-sdk/php/core/generated`
- `packages/client-sdk/python/*/src/*/_generated` and `packages/client-sdk/python/core/src/calyx_client_sdk_core/_proto`
- `packages/client-sdk/vb6/src/CalyxData.bas` and `packages/client-sdk/vb6/src/CalyxJson.bas`

## Conventions

- **Environment variables have two sources of truth:** `.env.example` and the tables "Values to
  fill in" and "Optional" of `README.md`. A change to a variable changes both. `docker-compose.yml`
  only reads the values and points to `.env.example`.
- VB6 files have CRLF line endings and ASCII characters only.
- Proto fields are `snake_case`.
- whatsmeow is reverse engineered. Check the installed source
  (`go list -m -f '{{.Dir}}' go.mau.fi/whatsmeow`) before you state how it behaves.
- Licenses: `apps/*` and the repository by default are AGPL-3.0-only. `packages/shared-types` and
  `packages/client-sdk` are Apache-2.0. See `NOTICE`.

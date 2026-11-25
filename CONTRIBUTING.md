# Contributing to Calyx

Thanks for your interest. A few rules keep the project maintainable.

## Ground rules

- **Security problems go in private.** Do not open a public issue for a vulnerability: follow
  [SECURITY.md](./SECURITY.md).
- **English only.** Code, comments, documentation, issues and pull requests. It is the one language
  everybody shares, and it avoids half-translated documentation between versions.
- **You own every line you send.** Tools can help you write code, but you must understand, test
  and explain every line you send. The maintainer closes a pull request with code that its author
  did not check, or with careless code that breaks other parts.
- **Guardrails are not negotiable.** Changes that remove, lower or make optional the pacing limits
  of the core, or that exist to make unsolicited bulk messaging harder to detect, are not accepted.
- **Small and readable.** One concern per pull request. No new dependency without discussing it
  first in an issue.

## What fits in Calyx

Calyx stays small on purpose. It is for people who want to use WhatsApp Web: mainly individuals and
small and medium companies with few resources. For them, the registration with Meta is too complex.
They want to send reminders to their customers, as they do by mail, with no complications. The
Meta Cloud API provider is for those who want the official API.

Calyx follows the Unix philosophy: do one thing and do it well. It connects WhatsApp numbers to
software, safely. A change fits when it is coherent with this goal and with the product principles:
guardrails first, reliability over features, and use only the layer you need.

These contributions fit:

- Bug fixes, with a test that fails before the fix.
- Changes that make the core or the backend more reliable.
- Changes that make the guardrails stronger.
- Corrections and improvements to the documentation.
- Fixes and improvements to the existing client SDKs.
- Small features that help the target users above.

These contributions do not fit:

- Integrations with chat platforms, for example Chatwoot. Chatwoot is a well-made project with
  another purpose: one inbox for many channels, which stores the conversations. Calyx does not
  build that again. If you want what Chatwoot gives, use Chatwoot. Calyx already gives an HTTP API
  and webhooks for each event, so a bridge can live outside Calyx, as a separate project.
- More providers or more integrations that the target users do not need.
- More message brokers, for example Kafka or RabbitMQ. Redis stays the one optional queue of the
  core, and the core works without it.
- Changes that remove, lower or make optional the guardrails.
- Features that help bulk unsolicited messaging.

Before you start work on a new feature, do these steps:

1. Open an issue that describes the feature and the problem that it solves.
2. Wait for the maintainer to agree.
3. Write the code, test-first.

Every line of code that you send must be code that you understand, can explain and have tested.

## Contributor License Agreement

Before a contribution can be merged you must sign the project's Contributor License Agreement
(CLA). It confirms that you have the right to contribute the code and lets the project distribute
it under its current licenses and under other terms, which is what makes a commercial edition
possible while the open source edition stays open.

The CLA text and the way to sign it will be linked here before the first external contribution is
accepted.

## Licenses

- `apps/*` and everything else by default: AGPL-3.0-only.
- `packages/shared-types` and `packages/client-sdk`: Apache-2.0, so they can be used from any
  backend, open or closed.

By contributing you agree that your contribution is distributed under the license of the part of
the repository it touches.

## Development

```bash
pnpm install
docker compose -f docker-compose.dev.yml --profile test up -d postgres-test   # database for the backend tests
pnpm --filter @calyx/core test
TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55432/postgres pnpm --filter @calyx/backend test
```

To run the Go services in Docker from the source, without new images, use
`docker-compose.dev.yml` (the first lines of that file show the command). Each component README
lists the rest of its commands, for example `apps/core/README.md`.

### Change an environment variable

`.env.example` and the tables "Values to fill in" and "Optional" of `README.md` list every value
of `.env`. When you add, change or remove a variable, change both in the same pull request.

### Change the product version

The `version` field of the root `package.json` is the one source of the product version. The
core, the backend, the panel and the client SDKs all use it.

1. Edit the `version` field of the root `package.json`.
2. Run `pnpm version:sync`. It writes the new version in each copy.
3. Run `packages/client-sdk/php/docker/run.sh composer-core`. It refreshes the lock file of the
   PHP core client. The `content-hash` of `composer.lock` comes from `composer.json`, so the old
   hash is wrong after a new version. This step needs Docker.
4. Review the diff of `packages/client-sdk/php/core/composer.lock`. Composer can also update other
   packages. Keep only the change of `calyx/client-sdk` and `content-hash`.
5. Run `pnpm version:check`. It fails when a copy has another version.
6. Before a release, run `pnpm version:image-check`. It builds the core and backend images. Then
   it checks `--version` in each. This step needs Docker.

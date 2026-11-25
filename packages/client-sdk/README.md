# Calyx client SDKs

Typed clients for Calyx. Each language has its own folder: `packages/client-sdk/<language>`.
The license of this folder is Apache-2.0.

| Language | Folder | Backend client | Core client | State |
|---|---|---|---|---|
| TypeScript | [`typescript`](./typescript) | yes | yes | Built (backend and core) |
| Go | [`go`](./go) | yes | yes | Built |
| VB6 | [`vb6`](./vb6) | yes (7 operations) | no | Built |
| PHP | [`php/client`](./php/client), [`php/core`](./php/core) | yes (`calyx/client-sdk`) | yes (`calyx/client-sdk-core`, needs `ext-grpc`) | Built |
| Python | [`python/backend`](./python/backend), [`python/core`](./python/core) | yes (`calyx-client-sdk`) | yes (`calyx-client-sdk-core`) | Built; hover texts and real runs wait for the author |
| Others | none | planned | no | Planned |

## Choose a client

1. Use the backend client when your program calls Calyx over HTTP with an API key. This is the
   normal choice.
2. Use the core client only when your program runs on the private network of the core. The core
   has no users and no tenants.
3. A language without gRPC has only the backend client. PHP and Python have the core client in a
   separate package, so a program that needs only the backend installs no gRPC.

## The rules for every language

Every SDK follows the shared contract in [`CONTRACT.md`](./CONTRACT.md). The test data that every
language reads is in [`testdata/`](./testdata). Each language lives in
`packages/client-sdk/<language>`, and its README lists its own runtime values.

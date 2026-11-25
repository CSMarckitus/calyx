# Test-only TLS files

These files protect nothing. Only `test/core.test.ts` uses them, against the fake core on
`127.0.0.1`. They were made once on 2026-11-20 with `openssl` (P-256 keys, 36,500 days): a
self-signed CA, a server certificate for `127.0.0.1` and `localhost`, and a client certificate,
both signed by that CA. The key of the CA was deleted. The "wrong" CA is a second self-signed CA
that signed nothing; its key was also deleted.

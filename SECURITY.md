# Security policy

## Supported versions

Only the latest version gets security fixes. The branch `main` always has it.

| Version | Supported |
|---|---|
| 0.1.x (latest) | ✅ |
| Older versions | ❌ |

## Report a vulnerability

> [!CAUTION]
> Do not open a public issue for a vulnerability. A public report shows the problem to everybody
> before a fix exists.

1. Open a private report on GitHub:
   [Report a vulnerability](https://github.com/CSMarckitus/calyx/security/advisories/new).
2. Describe the problem, the affected part (core, backend, panel or an SDK) and the version.
3. Add the steps to reproduce it. Remove every secret, token, password and phone number first.

I maintain Calyx alone, in my free time. I try to answer within one week, and I tell you when a
fix is ready. When you agree, the advisory names you.

## What counts as a vulnerability

- Access to the data or the instances of another organization or client (a break of the
  isolation between tenants).
- Access to the API with no valid login or API key.
- A way to skip or shorten the pacing of the core. The guardrails are a security feature.
- A leak of secrets: API keys, webhook secrets, tokens or passwords.

These are not vulnerabilities:

- A ban of a number by WhatsApp. Using WhatsApp Web through an unofficial client is against the
  Terms of Service of WhatsApp, and no software removes that risk.
- A problem in an installation that does not follow the documentation, for example a gRPC port
  of the core that is open to the internet.

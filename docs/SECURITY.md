# Security model

## Trust boundaries

- Agents are untrusted authenticated writers for exactly one node.
- Public visitors receive only visible nodes and explicitly public fields.
- Administrators can mutate configuration through cookie-authenticated, CSRF-protected APIs.
- Notification destinations are privileged secrets and are never returned after creation.

## Required controls

- Agent and session tokens are generated from a cryptographic RNG and stored as SHA-256
  hashes. Plaintext values are shown once.
- Administrator passwords use bcrypt with an upgrade path to Argon2id.
- Login endpoints are rate limited and introduce CAPTCHA after repeated failures.
- Session cookies are HttpOnly, SameSite=Lax, and Secure when TLS is enabled.
- Every state-changing cookie-authenticated request requires `X-CSRF-Token`.
- WebSocket messages and HTTP bodies have strict size limits and typed validation.
- Advanced custom HTML is sanitized and governed by a restrictive CSP.
- Production startup warns or fails when a public listener is configured without an
  explicit TLS/reverse-proxy acknowledgement.
- Database backups exclude agent tokens and notification secrets unless encrypted export
  is explicitly selected.

## Non-goals

The v1 agent cannot execute shell commands, open terminals, upload arbitrary files, or
accept inbound network connections.

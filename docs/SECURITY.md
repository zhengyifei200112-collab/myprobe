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
- Password-protected chart shares use separate, scope-bound HttpOnly sessions. Five
  failed passwords in ten minutes trigger a persistent fifteen-minute block per share
  and source IP. Share responses are private and non-cacheable, and every history query
  revalidates the selected-node allowlist on the server.
- Webhook URLs, Telegram Bot Tokens, and chat IDs are encrypted with AES-256-GCM before
  they are written to SQLite. `MYPROBE_ENCRYPTION_KEY` must contain at least 32
  characters and must be backed up separately; changing or losing it makes existing
  notification configurations unreadable.
- WebSocket messages and HTTP bodies have strict size limits and typed validation.
- Latency tasks accept only validated host names/IP addresses and fixed Ping/TCP probe
  implementations. The agent never passes task data through a shell, and results are
  accepted only for a matching unexpired task issued by the server.
- Advanced custom HTML is sanitized and governed by a restrictive CSP.
- Production startup warns or fails when a public listener is configured without an
  explicit TLS/reverse-proxy acknowledgement.
- Database backups exclude agent tokens and notification secrets unless encrypted export
  is explicitly selected.

Administrators are trusted to configure outbound notification destinations. Webhooks may
intentionally target private infrastructure, so deployments should apply egress firewall
rules when administrators must not be able to reach arbitrary internal HTTP services.

## Non-goals

The v1 agent cannot execute shell commands, open terminals, upload arbitrary files, or
accept inbound network connections.

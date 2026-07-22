# Security model

## Trust boundaries

- Agents are untrusted authenticated writers for exactly one node.
- Public visitors receive only visible nodes and explicitly public fields.
- Agent machine IDs are never persisted. Real hostnames, Agent versions, and capability
  lists are available to administrators but removed from public node responses; public
  cards receive only OS, platform, version, kernel, and architecture labels.
- Administrators can mutate configuration through cookie-authenticated, CSRF-protected APIs.
- Notification destinations are privileged secrets and are never returned after creation.

## Required controls

- Agent and session tokens are generated from a cryptographic RNG and stored as SHA-256
  hashes. Plaintext values are shown once.
- Administrator passwords use bcrypt and are rehashed whenever the password changes.
- Login endpoints are rate limited and introduce CAPTCHA after repeated failures.
- Login failure counters and one-time CAPTCHA challenges are persisted in SQLite. Three
  matching username/IP failures require CAPTCHA; five matching failures or ten failures
  from one IP impose a fifteen-minute block that survives service restarts. Unknown
  usernames still execute a dummy bcrypt comparison to reduce account-enumeration timing.
- Password changes require the current password, enforce a twelve-character minimum,
  reject reuse of the current value, and revoke every session for that administrator.
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
- Forwarded client-IP headers are ignored by default. Deployments may set
  `MYPROBE_TRUSTED_PROXIES` to an explicit comma-separated IP/CIDR allowlist; the legacy
  `MYPROBE_TRUST_PROXY=true` setting trusts loopback proxies only.
- Latency tasks accept only validated host names/IP addresses and fixed Ping/TCP probe
  implementations. The agent never passes task data through a shell, and results are
  accepted only for a matching unexpired task issued by the server.
- Advanced custom HTML is sanitized and governed by a restrictive CSP.
- Production startup warns or fails when a public listener is configured without an
  explicit TLS/reverse-proxy acknowledgement.
- Versioned JSON configuration exports exclude passwords, agent tokens, notification
  credentials, share password hashes, sessions, and history. Full database exports use
  scrypt-derived AES-256-GCM keys with independently authenticated chunks and an
  authenticated terminator, so wrong passwords, tampering, truncation, and trailing data
  are rejected. Restores are integrity-checked and staged while the service is running;
  the next startup activates them before opening SQLite and preserves the previous
  database as a timestamped recovery copy.

Administrators are trusted to configure outbound notification destinations. Webhooks may
intentionally target private infrastructure, so deployments should apply egress firewall
rules when administrators must not be able to reach arbitrary internal HTTP services.

## Non-goals

The v1 agent cannot execute shell commands, open terminals, upload arbitrary files, or
accept inbound network connections.

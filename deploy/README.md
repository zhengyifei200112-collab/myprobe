# Deployment

## Docker Compose

1. Copy `.env.example` to `.env`, replace the administrator password and encryption
   key, and adjust the trusted proxy CIDR to the actual container network.
2. Run `docker compose up -d --build`.
3. Put an HTTPS reverse proxy in front of `127.0.0.1:25775`. The proxy must support
   WebSocket upgrades and should pass `X-Forwarded-For`.
4. Keep `MYPROBE_COOKIE_SECURE=true` when the public URL is HTTPS. Set
   `MYPROBE_PUBLIC_HTTP_ACKNOWLEDGED=true` after the HTTPS proxy is in place, or only
   when deliberately accepting the risk of direct public HTTP.

The named `myprobe-data` volume contains SQLite and must be included in host backups.
Keep `MYPROBE_ENCRYPTION_KEY` in a separate secret backup because encrypted notification
credentials cannot be recovered without it.

## systemd

Install the release binaries as `/usr/local/bin/myprobe-server` and
`/usr/local/bin/myprobe-agent`. Install `myprobe.service` in `/etc/systemd/system/`, and
create `/etc/myprobe/server.env` with at least:

```ini
MYPROBE_ADMIN_PASSWORD=a-long-unique-password
MYPROBE_ENCRYPTION_KEY=at-least-32-random-characters
MYPROBE_COOKIE_SECURE=true
MYPROBE_TRUSTED_PROXIES=127.0.0.1,::1
```

Then run:

```sh
systemctl daemon-reload
systemctl enable --now myprobe.service
```

For each monitored machine, install `myprobe-agent@.service`, create a file such as
`/etc/myprobe/agents/tokyo.env`, and enable `myprobe-agent@tokyo.service`:

```ini
MYPROBE_SERVER=https://status.example.com
MYPROBE_TOKEN=the-one-time-node-token
```

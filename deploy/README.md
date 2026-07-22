# Deployment

MyProbe supports four maintained deployment paths:

| Method | Best for | Updates | Service management |
| --- | --- | --- | --- |
| One-click installer | Linux amd64/arm64 hosts | Re-run installer or use `update` | systemd |
| Published container | Docker/Compose hosts | Pull a new immutable release tag | Docker |
| Release binary | Custom or non-systemd environments | Replace verified binary | Operator-defined |
| Source build | Development and auditing | Rebuild from a reviewed commit | Operator-defined |

Every packaged method depends on a published GitHub Release. Release assets and
containers are produced only from version tags on the protected `main` branch.

## One-click Linux installer

Download the script first so it can be inspected before root execution:

```sh
curl -fsSL https://raw.githubusercontent.com/zhengyifei200112-collab/myprobe/main/install.sh -o install.sh
chmod +x install.sh
sudo ./install.sh server
```

The Server installer prompts for the initial administrator password, generates a
stable encryption key when one is not provided, installs the hardened systemd unit,
and listens on `0.0.0.0:25775` by default. After the host firewall allows TCP 25775,
open `http://SERVER_IP:25775`. Direct HTTP does not encrypt credentials or sessions.

For a domain behind an HTTPS reverse proxy, install with
`sudo ./install.sh server --reverse-proxy`. This binds `127.0.0.1:25775`, enables Secure
cookies, and trusts loopback proxies. The proxy must support WebSocket upgrades.

After creating a node in the administration console, install its Agent:

```sh
sudo ./install.sh agent
```

The Agent installer prompts for the HTTPS Server URL and the one-time node token.
Credentials are stored in root-readable environment files with mode `0600`; they
are never printed by the script.

Common lifecycle commands:

```sh
sudo ./install.sh update server
sudo ./install.sh update agent --name default
./install.sh status server
sudo ./install.sh uninstall agent --name default
```

Configuration and Server data are preserved by a normal uninstall. Use `--purge`
only when permanent removal is intended. For unattended provisioning, use secret files
such as `--admin-password-file`, `--encryption-key-file`, and `--token-file`;
see `./install.sh --help`.

## Docker Compose

1. Copy `.env.example` to `.env`, replace the administrator password and encryption
   key, and adjust the trusted proxy CIDR to the actual container network.
2. Choose either a local build or published image:

   ```sh
   # Local build from the checked-out source.
   docker compose up -d --build

   # Published image (set this in .env).
   MYPROBE_IMAGE=ghcr.io/zhengyifei200112-collab/myprobe:latest
   docker compose pull
   docker compose up -d --no-build
   ```

   Pin a version such as `v1.2.0` instead of `latest` when deterministic rollback
   is required.
3. The default `0.0.0.0:25775` mapping provides `http://SERVER_IP:25775`. Confirm the
   host firewall scope and use a strong unique administrator password.
4. For an HTTPS domain, bind `127.0.0.1`, enable `MYPROBE_COOKIE_SECURE`, and set
   `MYPROBE_TRUSTED_PROXIES` to the exact proxy address/CIDR. The proxy must support
   WebSocket upgrades and should pass `X-Forwarded-For`.

The named `myprobe-data` volume contains SQLite and must be included in host backups.
Keep `MYPROBE_ENCRYPTION_KEY` in a separate secret backup because encrypted notification
credentials cannot be recovered without it.

The Server listens on `:25775` inside its container so Docker port forwarding can reach
it. If the host mapping remains on `127.0.0.1`, the Server's public-HTTP startup warning
describes the container listener and does not mean the host port is public. Do not
silence the warning until HTTPS is actually in place or direct HTTP risk is deliberately
accepted.

### Linux host Agent container

Use the dedicated Agent image instead of overriding the Server image entrypoint. The
Agent image has no Server health check and includes the `ping` utility required for
Ping tasks.

```sh
cp deploy/agent.env.example .env.agent
chmod 0600 .env.agent
# Set MYPROBE_SERVER, MYPROBE_TOKEN, and pin MYPROBE_AGENT_IMAGE to a release tag.
docker compose --env-file .env.agent -f compose.agent.yaml pull
docker compose --env-file .env.agent -f compose.agent.yaml up -d --no-build
docker compose --env-file .env.agent -f compose.agent.yaml logs --tail=50 myprobe-agent
```

The template is Linux-only. It uses host networking for localhost Server access and
real network behavior, shares the host UTS namespace for the hostname, and bind-mounts
the host root read-only with `rslave` propagation. The `HOST_*` paths let gopsutil read
host `/proc`, `/sys`, and related metadata, while `MYPROBE_HOST_ROOT=/host` maps disk
statistics back to logical host mount names. It exposes no inbound ports, uses a
read-only container filesystem, drops all capabilities, and adds back only `NET_RAW`
for Ping tasks.

To build the Agent locally instead of pulling it, use:

```sh
docker compose --env-file .env.agent -f compose.agent.yaml up -d --build
```

Pin `MYPROBE_AGENT_IMAGE` to the same release tag as the Server. The Agent token remains
visible to Docker administrators through container configuration, so protect the env
file with mode `0600` and never commit it. Prefer the systemd or release-binary Agent on
hosts where root bind mounts or host namespaces are prohibited.

## Release binaries

Download the binary and `SHA256SUMS` from the same GitHub Release. Verify the exact
asset before installing it:

```sh
sha256sum --check --ignore-missing SHA256SUMS
install -m 0755 myprobe-server-linux-amd64 /usr/local/bin/myprobe-server
```

Linux Server and Agent assets are published for amd64 and arm64. Windows Server and
Agent binaries and macOS Agent binaries are also published. Non-systemd service
management remains the operator's responsibility.

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

The committed units are also the canonical templates embedded by `install.sh`. Any
unit change must update both surfaces and the installer validation in the same pull
request.

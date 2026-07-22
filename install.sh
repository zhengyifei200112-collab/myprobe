#!/usr/bin/env bash
set -Eeuo pipefail

readonly REPOSITORY="zhengyifei200112-collab/myprobe"
readonly INSTALL_DIR="/usr/local/bin"
readonly CONFIG_DIR="/etc/myprobe"
readonly SYSTEMD_DIR="/etc/systemd/system"
readonly SERVER_SERVICE="myprobe.service"
readonly AGENT_SERVICE_TEMPLATE="myprobe-agent@.service"

VERSION="latest"
ROLE=""
ACTION="install"
AGENT_NAME="default"
SERVER_URL=""
TOKEN_FILE=""
ADMIN_PASSWORD_FILE=""
ENCRYPTION_KEY_FILE=""
LISTEN_ADDRESS=""
REVERSE_PROXY=0
COOKIE_SECURE=""
TRUSTED_PROXIES=""
PUBLIC_HTTP_ACKNOWLEDGED=""
FORCE_CONFIG=0
PURGE=0
NO_START=0
TEMP_DIR=""
DOWNLOADED_BINARY=""

log() {
  printf '[MyProbe] %s\n' "$*" >&2
}

warn() {
  printf '[MyProbe] WARNING: %s\n' "$*" >&2
}

die() {
  printf '[MyProbe] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
MyProbe Linux installer

Usage:
  sudo ./install.sh [server|agent] [options]
  sudo ./install.sh update [server|agent] [options]
  sudo ./install.sh uninstall [server|agent] [options]
  ./install.sh status [server|agent] [options]

With no arguments, the installer starts an interactive setup.

Common options:
  --version VERSION               Release tag, for example v1.0.0 (default: latest)
  --no-start                      Install files without enabling or starting services
  -h, --help                      Show this help

Server options:
  --listen ADDRESS                Listen address (default: 0.0.0.0:25775)
  --reverse-proxy                 Default to loopback and HTTPS-only proxy cookies
  --admin-password-file PATH      Read the initial administrator password from a file
  --encryption-key-file PATH      Read the stable encryption key from a file
  --force-config                  Replace an existing server.env

Agent options:
  --name NAME                     systemd instance name (default: default)
  --server-url URL                MyProbe Server HTTPS URL
  --token-file PATH               Read the Agent token from a file
  --force-config                  Replace an existing Agent environment file

Uninstall options:
  --purge                         Also remove configuration; for Server, remove data

Environment alternatives for first-time setup:
  MYPROBE_ADMIN_USERNAME, MYPROBE_ADMIN_PASSWORD, MYPROBE_ENCRYPTION_KEY
  MYPROBE_SERVER, MYPROBE_TOKEN

Examples:
  sudo ./install.sh server
  sudo ./install.sh server --reverse-proxy
  sudo ./install.sh agent
  sudo ./install.sh update server --version v1.2.0
  sudo ./install.sh uninstall agent --name default
EOF
}

cleanup() {
  if [[ -n "${TEMP_DIR}" && -d "${TEMP_DIR}" ]]; then
    rm -rf -- "${TEMP_DIR}"
  fi
}

trap cleanup EXIT

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

require_root() {
  [[ "${EUID}" -eq 0 ]] || die "Run this command as root (for example with sudo)."
}

require_linux_systemd() {
  [[ "$(uname -s)" == "Linux" ]] || die "The one-click installer supports Linux only. Use a release binary on other systems."
  command -v systemctl >/dev/null 2>&1 || die "systemd is required. Use Docker or a release binary on this host."
}

normalize_arch() {
  case "$1" in
    x86_64 | amd64) printf 'amd64\n' ;;
    aarch64 | arm64) printf 'arm64\n' ;;
    *) return 1 ;;
  esac
}

validate_version() {
  [[ "$1" == "latest" || "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]
}

validate_agent_name() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$ ]]
}

validate_single_line() {
  local value="$1"
  [[ -n "${value}" && "${value}" != *$'\n'* && "${value}" != *$'\r'* ]]
}

systemd_quote() {
  local value="$1"
  validate_single_line "${value}" || return 1
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "${value}"
}

read_secret_file() {
  local path="$1"
  [[ -f "${path}" ]] || die "Secret file does not exist: ${path}"
  local value
  value="$(<"${path}")"
  validate_single_line "${value}" || die "Secret file must contain one non-empty line: ${path}"
  printf '%s' "${value}"
}

prompt_secret_twice() {
  local label="$1"
  local first second
  read -r -s -p "${label}: " first
  printf '\n' >&2
  read -r -s -p "Confirm ${label}: " second
  printf '\n' >&2
  [[ -n "${first}" ]] || die "${label} cannot be empty."
  [[ "${first}" == "${second}" ]] || die "${label} values did not match."
  printf '%s' "${first}"
}

generate_encryption_key() {
  require_command od
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

resolve_server_access_defaults() {
  if [[ -z "${LISTEN_ADDRESS}" ]]; then
    if [[ "${REVERSE_PROXY}" -eq 1 ]]; then
      LISTEN_ADDRESS="127.0.0.1:25775"
    else
      LISTEN_ADDRESS="0.0.0.0:25775"
    fi
  fi
  if [[ "${REVERSE_PROXY}" -eq 1 ]]; then
    COOKIE_SECURE="true"
    TRUSTED_PROXIES="127.0.0.1,::1"
    PUBLIC_HTTP_ACKNOWLEDGED="false"
  else
    COOKIE_SECURE="false"
    TRUSTED_PROXIES=""
    PUBLIC_HTTP_ACKNOWLEDGED="true"
  fi
}

release_base_url() {
  if [[ "${VERSION}" == "latest" ]]; then
    printf 'https://github.com/%s/releases/latest/download\n' "${REPOSITORY}"
  else
    printf 'https://github.com/%s/releases/download/%s\n' "${REPOSITORY}" "${VERSION}"
  fi
}

download_and_verify() {
  local component="$1"
  local arch asset base expected actual
  arch="$(normalize_arch "$(uname -m)")" || die "Unsupported CPU architecture: $(uname -m). Supported: amd64, arm64."
  asset="myprobe-${component}-linux-${arch}"
  base="$(release_base_url)"
  TEMP_DIR="$(mktemp -d)"

  log "Downloading ${asset} from ${VERSION} release..."
  if ! curl --fail --location --silent --show-error "${base}/${asset}" -o "${TEMP_DIR}/${asset}"; then
    die "Release asset not found. MyProbe needs a published GitHub Release before one-click installation can work."
  fi
  curl --fail --location --silent --show-error "${base}/SHA256SUMS" -o "${TEMP_DIR}/SHA256SUMS" ||
    die "Could not download SHA256SUMS; refusing an unverified installation."

  expected="$(awk -v name="${asset}" '$2 == name || $2 == "*" name { print $1; exit }' "${TEMP_DIR}/SHA256SUMS")"
  [[ "${expected}" =~ ^[0-9a-fA-F]{64}$ ]] || die "No valid checksum was published for ${asset}."
  actual="$(sha256sum "${TEMP_DIR}/${asset}" | awk '{print $1}')"
  [[ "${actual,,}" == "${expected,,}" ]] || die "Checksum verification failed for ${asset}."

  log "SHA-256 verified."
  DOWNLOADED_BINARY="${TEMP_DIR}/${asset}"
}

install_server_unit() {
  local temporary
  temporary="$(mktemp)"
  cat >"${temporary}" <<'EOF'
[Unit]
Description=MyProbe monitoring server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
DynamicUser=yes
StateDirectory=myprobe
Environment=MYPROBE_LISTEN=0.0.0.0:25775
Environment=MYPROBE_DATABASE=/var/lib/myprobe/myprobe.db
EnvironmentFile=/etc/myprobe/server.env
ExecStart=/usr/local/bin/myprobe-server
Restart=on-failure
RestartSec=5s
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes

[Install]
WantedBy=multi-user.target
EOF
  install -m 0644 "${temporary}" "${SYSTEMD_DIR}/${SERVER_SERVICE}"
  rm -f -- "${temporary}"
}

install_agent_unit() {
  local temporary
  temporary="$(mktemp)"
  cat >"${temporary}" <<'EOF'
[Unit]
Description=MyProbe agent (%i)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
DynamicUser=yes
EnvironmentFile=/etc/myprobe/agents/%i.env
ExecStart=/usr/local/bin/myprobe-agent
Restart=always
RestartSec=5s
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF
  install -m 0644 "${temporary}" "${SYSTEMD_DIR}/${AGENT_SERVICE_TEMPLATE}"
  rm -f -- "${temporary}"
}

write_server_config() {
  local path="${CONFIG_DIR}/server.env"
  if [[ -f "${path}" && "${FORCE_CONFIG}" -ne 1 ]]; then
    log "Preserving existing ${path}."
    return
  fi

  local username="${MYPROBE_ADMIN_USERNAME:-admin}"
  local password="${MYPROBE_ADMIN_PASSWORD:-}"
  local encryption_key="${MYPROBE_ENCRYPTION_KEY:-}"

  if [[ -n "${ADMIN_PASSWORD_FILE}" ]]; then
    password="$(read_secret_file "${ADMIN_PASSWORD_FILE}")"
  elif [[ -z "${password}" ]]; then
    [[ -t 0 ]] || die "Set MYPROBE_ADMIN_PASSWORD or use --admin-password-file for non-interactive setup."
    password="$(prompt_secret_twice "Initial administrator password")"
  fi

  if [[ -n "${ENCRYPTION_KEY_FILE}" ]]; then
    encryption_key="$(read_secret_file "${ENCRYPTION_KEY_FILE}")"
  elif [[ -z "${encryption_key}" ]]; then
    encryption_key="$(generate_encryption_key)"
    log "Generated a stable notification encryption key."
  fi

  [[ ${#password} -ge 12 ]] || die "Administrator password must contain at least 12 characters."
  [[ ${#encryption_key} -ge 32 ]] || die "Encryption key must contain at least 32 characters."
  validate_single_line "${username}" || die "Administrator username must be one non-empty line."
  validate_single_line "${LISTEN_ADDRESS}" || die "Listen address must be one non-empty line."

  umask 077
  {
    printf 'MYPROBE_ADMIN_USERNAME=%s\n' "$(systemd_quote "${username}")"
    printf 'MYPROBE_ADMIN_PASSWORD=%s\n' "$(systemd_quote "${password}")"
    printf 'MYPROBE_ENCRYPTION_KEY=%s\n' "$(systemd_quote "${encryption_key}")"
    printf 'MYPROBE_LISTEN=%s\n' "$(systemd_quote "${LISTEN_ADDRESS}")"
    printf 'MYPROBE_COOKIE_SECURE=%s\n' "${COOKIE_SECURE}"
    printf 'MYPROBE_TRUSTED_PROXIES=%s\n' "$(systemd_quote "${TRUSTED_PROXIES}")"
    printf 'MYPROBE_PUBLIC_HTTP_ACKNOWLEDGED=%s\n' "${PUBLIC_HTTP_ACKNOWLEDGED}"
  } >"${path}.tmp"
  chmod 0600 "${path}.tmp"
  mv -f -- "${path}.tmp" "${path}"
  log "Created ${path} with mode 0600."
}

write_agent_config() {
  local agents_dir="${CONFIG_DIR}/agents"
  local path="${agents_dir}/${AGENT_NAME}.env"
  install -d -m 0700 "${agents_dir}"

  if [[ -f "${path}" && "${FORCE_CONFIG}" -ne 1 ]]; then
    log "Preserving existing ${path}."
    return
  fi

  local server="${SERVER_URL:-${MYPROBE_SERVER:-}}"
  local token="${MYPROBE_TOKEN:-}"
  if [[ -n "${TOKEN_FILE}" ]]; then
    token="$(read_secret_file "${TOKEN_FILE}")"
  fi
  if [[ -z "${server}" ]]; then
    [[ -t 0 ]] || die "Set MYPROBE_SERVER or use --server-url for non-interactive setup."
    read -r -p "MyProbe Server URL (HTTPS): " server
  fi
  if [[ -z "${token}" ]]; then
    [[ -t 0 ]] || die "Set MYPROBE_TOKEN or use --token-file for non-interactive setup."
    read -r -s -p "Agent token: " token
    printf '\n' >&2
  fi

  validate_single_line "${server}" || die "Server URL must be one non-empty line."
  validate_single_line "${token}" || die "Agent token must be one non-empty line."
  [[ "${server}" =~ ^https:// ]] || warn "The Agent Server URL is not HTTPS. Use plaintext HTTP only on a trusted private network."

  umask 077
  {
    printf 'MYPROBE_SERVER=%s\n' "$(systemd_quote "${server}")"
    printf 'MYPROBE_TOKEN=%s\n' "$(systemd_quote "${token}")"
  } >"${path}.tmp"
  chmod 0600 "${path}.tmp"
  mv -f -- "${path}.tmp" "${path}"
  log "Created ${path} with mode 0600."
}

install_role() {
  require_root
  require_linux_systemd
  require_command curl
  require_command awk
  require_command sha256sum
  require_command mktemp
  require_command install

  install -d -m 0755 "${CONFIG_DIR}"
  download_and_verify "${ROLE}"
  install -m 0755 "${DOWNLOADED_BINARY}" "${INSTALL_DIR}/myprobe-${ROLE}"

  if [[ "${ROLE}" == "server" ]]; then
    install_server_unit
    write_server_config
  else
    validate_agent_name "${AGENT_NAME}" || die "Invalid Agent name. Use 1-64 letters, digits, dots, underscores, or hyphens."
    install_agent_unit
    write_agent_config
  fi

  systemctl daemon-reload
  if [[ "${NO_START}" -eq 1 ]]; then
    log "Installed MyProbe ${ROLE}; service start was skipped."
  elif [[ "${ROLE}" == "server" ]]; then
    systemctl enable "${SERVER_SERVICE}"
    systemctl restart "${SERVER_SERVICE}"
    log "MyProbe Server is installed and running."
  else
    systemctl enable "myprobe-agent@${AGENT_NAME}.service"
    systemctl restart "myprobe-agent@${AGENT_NAME}.service"
    log "MyProbe Agent instance '${AGENT_NAME}' is installed and running."
  fi
}

uninstall_role() {
  require_root
  require_linux_systemd

  if [[ "${ROLE}" == "server" ]]; then
    systemctl disable --now "${SERVER_SERVICE}" >/dev/null 2>&1 || true
    rm -f -- "${SYSTEMD_DIR}/${SERVER_SERVICE}" "${INSTALL_DIR}/myprobe-server"
    if [[ "${PURGE}" -eq 1 ]]; then
      rm -f -- "${CONFIG_DIR}/server.env"
      rm -rf -- /var/lib/myprobe
      log "Removed Server configuration and data because --purge was specified."
    else
      log "Preserved Server configuration and data."
    fi
  else
    validate_agent_name "${AGENT_NAME}" || die "Invalid Agent name."
    systemctl disable --now "myprobe-agent@${AGENT_NAME}.service" >/dev/null 2>&1 || true
    if [[ "${PURGE}" -eq 1 ]]; then
      rm -f -- "${CONFIG_DIR}/agents/${AGENT_NAME}.env"
    fi
    if ! find "${CONFIG_DIR}/agents" -maxdepth 1 -type f -name '*.env' -print -quit 2>/dev/null | grep -q .; then
      rm -f -- "${SYSTEMD_DIR}/${AGENT_SERVICE_TEMPLATE}" "${INSTALL_DIR}/myprobe-agent"
    fi
    if [[ "${PURGE}" -eq 1 ]]; then
      log "Removed Agent instance configuration."
    else
      log "Preserved Agent instance configuration."
    fi
  fi
  systemctl daemon-reload
  log "MyProbe ${ROLE} was uninstalled."
}

show_status() {
  require_linux_systemd
  if [[ "${ROLE}" == "server" ]]; then
    systemctl status "${SERVER_SERVICE}" --no-pager
  else
    validate_agent_name "${AGENT_NAME}" || die "Invalid Agent name."
    systemctl status "myprobe-agent@${AGENT_NAME}.service" --no-pager
  fi
}

interactive_role() {
  [[ -t 0 ]] || die "Specify server or agent in non-interactive mode."
  printf 'Install which MyProbe component?\n  1) Server\n  2) Agent\n'
  local choice
  read -r -p "Choice [1]: " choice
  case "${choice:-1}" in
    1 | server) ROLE="server" ;;
    2 | agent) ROLE="agent" ;;
    *) die "Invalid choice." ;;
  esac
}

parse_arguments() {
  if [[ $# -eq 0 ]]; then
    interactive_role
    return
  fi

  case "$1" in
    install) ACTION="install"; shift ;;
    update) ACTION="install"; shift ;;
    uninstall) ACTION="uninstall"; shift ;;
    status) ACTION="status"; shift ;;
    -h | --help) usage; exit 0 ;;
  esac

  if [[ $# -gt 0 && ( "$1" == "server" || "$1" == "agent" ) ]]; then
    ROLE="$1"
    shift
  elif [[ "${ACTION}" == "install" ]]; then
    interactive_role
  else
    die "Specify server or agent."
  fi

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version)
        [[ $# -ge 2 ]] || die "--version requires a value."
        VERSION="$2"
        shift 2
        ;;
      --name)
        [[ $# -ge 2 ]] || die "--name requires a value."
        AGENT_NAME="$2"
        shift 2
        ;;
      --server-url)
        [[ $# -ge 2 ]] || die "--server-url requires a value."
        SERVER_URL="$2"
        shift 2
        ;;
      --token-file)
        [[ $# -ge 2 ]] || die "--token-file requires a value."
        TOKEN_FILE="$2"
        shift 2
        ;;
      --admin-password-file)
        [[ $# -ge 2 ]] || die "--admin-password-file requires a value."
        ADMIN_PASSWORD_FILE="$2"
        shift 2
        ;;
      --encryption-key-file)
        [[ $# -ge 2 ]] || die "--encryption-key-file requires a value."
        ENCRYPTION_KEY_FILE="$2"
        shift 2
        ;;
      --listen)
        [[ $# -ge 2 ]] || die "--listen requires a value."
        LISTEN_ADDRESS="$2"
        shift 2
        ;;
      --reverse-proxy) REVERSE_PROXY=1; shift ;;
      --force-config) FORCE_CONFIG=1; shift ;;
      --purge) PURGE=1; shift ;;
      --no-start) NO_START=1; shift ;;
      -h | --help) usage; exit 0 ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  validate_version "${VERSION}" || die "Version must be latest or a tag such as v1.2.3."
  [[ "${ROLE}" == "server" || "${ROLE}" == "agent" ]] || die "Role must be server or agent."
  if [[ "${ROLE}" == "server" ]]; then
    resolve_server_access_defaults
  fi
}

main() {
  parse_arguments "$@"
  case "${ACTION}" in
    install) install_role ;;
    uninstall) uninstall_role ;;
    status) show_status ;;
    *) die "Unsupported action: ${ACTION}" ;;
  esac
}

if [[ "${MYPROBE_INSTALLER_TESTING:-0}" != "1" ]]; then
  main "$@"
fi

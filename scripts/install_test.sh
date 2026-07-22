#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MYPROBE_INSTALLER_TESTING=1 source "${ROOT_DIR}/install.sh"

failures=0

assert_equal() {
  local expected="$1"
  local actual="$2"
  local name="$3"
  if [[ "${expected}" != "${actual}" ]]; then
    printf 'not ok - %s (expected %q, got %q)\n' "${name}" "${expected}" "${actual}" >&2
    failures=$((failures + 1))
  else
    printf 'ok - %s\n' "${name}"
  fi
}

assert_success() {
  local name="$1"
  shift
  if "$@"; then
    printf 'ok - %s\n' "${name}"
  else
    printf 'not ok - %s\n' "${name}" >&2
    failures=$((failures + 1))
  fi
}

assert_failure() {
  local name="$1"
  shift
  if "$@"; then
    printf 'not ok - %s\n' "${name}" >&2
    failures=$((failures + 1))
  else
    printf 'ok - %s\n' "${name}"
  fi
}

extract_unit() {
  local function_name="$1"
  awk -v function_name="${function_name}" '
    $0 == function_name "() {" { in_function = 1 }
    in_function && /^  cat .*<<.EOF.$/ { in_unit = 1; next }
    in_unit && /^EOF$/ { exit }
    in_unit { sub(/\r$/, ""); print }
  ' "${ROOT_DIR}/install.sh"
}

assert_equal "amd64" "$(normalize_arch x86_64)" "maps x86_64"
assert_equal "arm64" "$(normalize_arch aarch64)" "maps aarch64"
assert_failure "rejects unsupported architecture" normalize_arch riscv64
assert_success "accepts latest version" validate_version latest
assert_success "accepts semantic release tag" validate_version v1.2.3
assert_failure "rejects unsafe release tag" validate_version '../main'
assert_success "accepts safe Agent name" validate_agent_name edge-01
assert_failure "rejects Agent path traversal" validate_agent_name '../edge'
assert_equal '"a b\"c\\d"' "$(systemd_quote 'a b"c\d')" "quotes systemd environment value"
assert_failure "rejects multiline environment value" validate_single_line $'first\nsecond'
assert_equal "$(tr -d '\r' <"${ROOT_DIR}/deploy/myprobe.service")" "$(extract_unit install_server_unit)" "Server unit matches deploy template"
assert_equal "$(tr -d '\r' <"${ROOT_DIR}/deploy/myprobe-agent@.service")" "$(extract_unit install_agent_unit)" "Agent unit matches deploy template"

if [[ "${failures}" -ne 0 ]]; then
  printf '%d installer test(s) failed\n' "${failures}" >&2
  exit 1
fi

printf 'all installer tests passed\n'

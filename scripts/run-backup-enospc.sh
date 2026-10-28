#!/bin/sh
# Run a compiled Linux backup test binary against a disposable 128 MiB tmpfs.
set -eu
binary=${1:?provide an absolute compiled Linux backup test executable}
case "$binary" in /*) ;; *) printf '%s\n' 'Expected an absolute executable path.' >&2; exit 2 ;; esac
[ -x "$binary" ] || { printf '%s\n' 'Backup test executable is not executable.' >&2; exit 2; }
fixture_root=$(mktemp -d /tmp/myprobe-backup-enospc.XXXXXXXX)
case "$fixture_root" in /tmp/myprobe-backup-enospc.*) ;; *) printf '%s\n' 'Unexpected fixture path.' >&2; exit 2 ;; esac
cleanup() {
  if mountpoint -q "$fixture_root"; then umount "$fixture_root"; fi
  rmdir "$fixture_root"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
mount -t tmpfs -o size=128m,mode=0700 myprobe-backup-enospc "$fixture_root"
MYPROBE_BACKUP_ENOSPC_DIRECTORY="$fixture_root" "$binary" -test.v -test.count=1 -test.timeout=120s

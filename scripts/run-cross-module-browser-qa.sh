#!/bin/sh
# A fresh namespace contains all private HTTP targets; no host networking changes.
set -eu
if [ "$(id -u)" != 0 ] || [ "$#" != 3 ]; then
    echo 'Usage as root: run-cross-module-browser-qa.sh <empty output> <httpapi.test> <agentclient.test>' >&2
    exit 1
fi
script_directory=$(dirname "$(realpath "$0")")
output=$(realpath "$1")
admin_binary=$(realpath "$2")
services_binary=$(realpath "$3")
# These parameters are intentionally expanded by the isolated child shell.
# shellcheck disable=SC2016
exec unshare --net sh -eu -c '
    ip link set lo up
    ip addr add 10.77.0.1/32 dev lo
    exec node "$1/run-cross-module-browser-qa.cjs" "$2" "$3" "$4"
' cross-module-browser "$script_directory" "$output" "$admin_binary" "$services_binary"

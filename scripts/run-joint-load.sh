#!/bin/sh
# Only changes addresses inside a newly created network namespace. The real
# Server keeps its normal notification SSRF checks and default HTTP sender.
set -eu
if [ "$(id -u)" != 0 ]; then
    echo 'Run this disposable fixture as root (network namespace required).' >&2
    exit 1
fi
if [ "$#" != 2 ]; then
    echo 'Usage: run-joint-load.sh /absolute/agentclient.test /absolute/server' >&2
    exit 1
fi
test_binary=$(realpath "$1")
server_binary=$(realpath "$2")
test -x "$test_binary"
test -x "$server_binary"
script_directory=$(dirname "$(realpath "$0")")
cd "$script_directory/../internal/agentclient"
export MYPROBE_TEST_JOINT_LOAD=1 MYPROBE_TEST_LOAD_NETWORK_NAMESPACE=1
export MYPROBE_TEST_LOAD_SERVER_BINARY="$server_binary"
# These variables are intentionally expanded by the child namespace shell.
# shellcheck disable=SC2016
exec unshare --net sh -eu -c '
    ip link set lo up
    ip addr add 192.0.2.1/32 dev lo
    ip addr add 10.77.0.1/32 dev lo
    exec "$1" -test.run="^TestJointRuntimeLoad$" -test.timeout="${MYPROBE_TEST_LOAD_TIMEOUT:-10m}" -test.v
' joint-load "$test_binary"

#!/bin/sh
# Silence only the fixture's disposable network namespace, never the host.
set -eu
if [ "$(id -u)" != 0 ] || [ "$#" != 1 ]; then
    echo 'Usage as root: run-network-evidence-loss.sh /absolute/agentclient.test' >&2
    exit 1
fi
test_binary=$(realpath "$1")
test -x "$test_binary"
# shellcheck disable=SC2016
exec unshare --net sh -eu -c '
    ip link set lo up
    ip addr add 192.0.2.53/32 dev lo
    sysctl -q -w net.ipv4.icmp_echo_ignore_all=1
    export MYPROBE_TEST_NETWORK_EVIDENCE_LOSS=1
    exec "$1" -test.run="^TestNetworkProbeNative(SilentTarget|NoRoute)$" -test.timeout=30s -test.v
' network-evidence "$test_binary"

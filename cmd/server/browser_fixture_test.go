package main

import (
	"math/rand/v2"
	"net"
	"strconv"
	"testing"
)

// Windows may return a low ephemeral port blocked by Chromium (for example
// 6697). Keep disposable browser Servers in the IANA dynamic/private range.
func browserFixtureAddress(t *testing.T) string {
	t.Helper()
	for i := 0; i < 32; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(49152+rand.IntN(16384)))
		if err != nil {
			continue
		}
		address := listener.Addr().String()
		listener.Close()
		return address
	}
	t.Fatal("could not reserve a browser fixture port")
	return ""
}

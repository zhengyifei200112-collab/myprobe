package httpprobe

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"testing"
	"time"
)

func TestExecutorTargetTransportFailures(t *testing.T) {
	for _, name := range []string{"closed-before-headers", "reset", "truncated-body"} {
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				request, err := http.ReadRequest(bufio.NewReader(conn))
				if err == nil {
					request.Body.Close()
					if name == "reset" {
						err = conn.(*net.TCPConn).SetLinger(0)
					} else if name == "truncated-body" {
						_, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 20\r\nConnection: close\r\n\r\nshort")
					}
				}
				done <- err
			}()
			e := New(Policy{})
			e.dialer.resolver = &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
			e.dialer.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
			}
			task := executorTask("http://service.example/")
			result := e.Execute(context.Background(), task)
			if result.Outcome != "failure" || result.ErrorClass != "transport" {
				t.Fatalf("target failure excluded from observations: %+v", result)
			}
			if err := result.ValidateFor(task, time.Now()); err != nil {
				t.Fatalf("transport result rejected: %v", err)
			}
			if name == "truncated-body" && result.StatusCode != 200 {
				t.Fatalf("lost received status: %+v", result)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTransportClassificationPreservesLocalFailures(t *testing.T) {
	for _, cause := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE} {
		err := fmt.Errorf("wrapped: %w", cause)
		if outcome, class := classifyError(context.Background(), err); outcome != "failure" || class != "transport" {
			t.Fatalf("%v: %s/%s", cause, outcome, class)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if outcome, class := classifyError(ctx, err); outcome != "unobserved" || class != "cancelled" {
			t.Fatalf("cancelled parent: %s/%s", outcome, class)
		}
	}
	if outcome, class := classifyError(context.Background(), &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EMFILE}); outcome != "unobserved" || class != "internal" {
		t.Fatalf("local resource failure counted against target: %s/%s", outcome, class)
	}
}

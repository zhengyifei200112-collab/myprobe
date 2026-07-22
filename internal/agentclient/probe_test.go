package agentclient

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestExecuteTCPTask(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	now := time.Now().UTC()
	result := executeTask(context.Background(), protocol.Task{
		ID: "task", Kind: protocol.TaskKindTCPing, TargetID: "target", Host: "127.0.0.1",
		Port: port, TimeoutMS: 1000, ExpiresAt: now.Add(time.Minute),
	})
	if !result.Success || result.LatencyMS < 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestPingCommandArgumentsArePlatformSpecificAndHostIsPositional(t *testing.T) {
	tests := []struct {
		goos string
		want []string
	}{
		{goos: "windows", want: []string{"-n", "1", "-w", "1500", "example.com"}},
		{goos: "darwin", want: []string{"-c", "1", "-W", "1500", "example.com"}},
		{goos: "linux", want: []string{"-c", "1", "-W", "2", "example.com"}},
	}
	for _, test := range tests {
		_, arguments := pingCommandForOS(test.goos, "example.com", 1500)
		if !reflect.DeepEqual(arguments, test.want) {
			t.Fatalf("%s arguments = %#v, want %#v", test.goos, arguments, test.want)
		}
	}
}

func TestExecuteTaskRejectsExpiredTask(t *testing.T) {
	result := executeTask(context.Background(), protocol.Task{
		ID: "task", Kind: protocol.TaskKindPing, TargetID: "target", Host: "127.0.0.1",
		TimeoutMS: 1000, ExpiresAt: time.Now().Add(-time.Second),
	})
	if result.ErrorClass != "invalid_task" {
		t.Fatalf("error class = %q", result.ErrorClass)
	}
}

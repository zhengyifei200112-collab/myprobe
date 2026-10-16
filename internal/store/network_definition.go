package store

import (
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func networkDefinition(kind, family string, packets int) (string, int) {
	if family == "" {
		family = check.FamilyAuto
	}
	if kind == protocol.TaskKindPing && packets == 0 {
		packets = 1
	}
	return family, packets
}

func validNetworkDefinition(task protocol.Task, family string, packets int) bool {
	// Older private configurations could retain an unused Ping port. Preserve
	// that metadata while ensuring it never enters the stricter v2 wire task.
	if task.Kind == protocol.TaskKindPing {
		task.Port = 0
	}
	return (check.Task{Task: task, AddressFamily: family, PacketCount: packets}).Validate(time.Now().UTC()) == nil
}

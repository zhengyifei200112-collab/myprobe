package agentclient

import (
	"errors"
	"os"
	"strings"
)

const jointRuntimeGCLogLimit = 32 << 20

// This switch belongs to the disposable load driver, never to the Server's
// product configuration. Trace output shares the existing private log only.
func jointRuntimeGCTraceSettings(directory string) (bool, error) {
	switch os.Getenv("MYPROBE_TEST_LOAD_RUNTIME_GC_TRACE") {
	case "":
		return false, nil
	case "1":
		if err := validateJointPrivateDirectory(directory); err != nil {
			return false, errors.New("GC tracing requires a private POSIX process log directory")
		}
		return true, nil
	default:
		return false, errors.New("runtime GC trace switch must be unset or 1")
	}
}

func jointRuntimeGCEnvironment(input []string, enabled bool) []string {
	result := make([]string, 0, len(input)+1)
	for _, value := range input {
		if enabled && strings.HasPrefix(value, "GODEBUG=") {
			continue
		}
		result = append(result, value)
	}
	if enabled {
		result = append(result, "GODEBUG=gctrace=1")
	}
	return result
}

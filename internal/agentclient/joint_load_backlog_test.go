package agentclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const jointMaxBacklogHours = 365 * 24

func parseJointBacklogHours(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	hours, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || hours < 0 || hours > jointMaxBacklogHours {
		return 0, errors.New("joint history backlog must be an integer from 0 to 8760 hours")
	}
	return hours, nil
}

func jointHistoryAnchor(started time.Time, backlogHours int) time.Time {
	return started.UTC().Add(-time.Duration(backlogHours) * time.Hour).Truncate(time.Hour)
}

// Only allocate a fresh owned child. No caller-supplied database is opened.
// The cleanup revalidates its absolute canonical target before recursive removal.
func newPrivateJointFixture(parent string) (string, func(bool) error, error) {
	if err := validateJointPrivateDirectory(parent); err != nil {
		return "", nil, err
	}
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", nil, err
	}
	root, err := os.MkdirTemp(canonical, "joint-fixture-")
	if err != nil {
		return "", nil, err
	}
	finish := func(keep bool) error {
		if keep {
			return nil
		}
		info, err := os.Lstat(root)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || filepath.Dir(root) != canonical {
			return errors.New("private fixture cleanup target changed")
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil || resolved != root {
			return errors.New("private fixture cleanup target is no longer canonical")
		}
		return os.RemoveAll(root)
	}
	return root, finish, nil
}

func jointFixtureRoot(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("MYPROBE_TEST_LOAD_PRIVATE_FIXTURE_DIR")
	if parent == "" {
		return t.TempDir()
	}
	root, finish, err := newPrivateJointFixture(parent)
	if err != nil {
		t.Fatal("cannot create private synthetic fixture")
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("failed test-owned fixture retained in the selected private directory")
		}
		if err := finish(t.Failed()); err != nil {
			t.Error("private fixture cleanup refused or failed")
		}
	})
	return root
}

func recordJointFixturePlan(root, binary string, nodes, backlogHours int, dense bool) error {
	server, err := os.Open(binary)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, server)
	server.Close()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(root, "fixture-plan.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(struct {
		Format       string    `json:"format"`
		CreatedAt    time.Time `json:"created_at"`
		ServerSHA256 string    `json:"server_sha256"`
		Nodes        int       `json:"nodes"`
		BacklogHours int       `json:"backlog_hours"`
		DenseYear    bool      `json:"dense_year"`
	}{"myprobe.joint-fixture-plan.v1", time.Now().UTC(), hex.EncodeToString(hash.Sum(nil)), nodes, backlogHours, dense})
}

func recordJointHistorySeed(root string, history jointHistorySeed) error {
	file, err := os.OpenFile(filepath.Join(root, "history-fixture.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(history)
}

package store

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

func historyReadMappedRegions(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		// Match only the database itself, excluding its intentionally retained
		// writer-owned SHM mapping and any unrelated fixture.
		if strings.HasSuffix(line, " "+path) {
			count++
		}
	}
	return count
}

func TestHistoryReadMappingsReleasedWithRows(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("mapped reader is enabled on 64-bit Linux only")
	}
	for _, disabled := range []bool{false, true} {
		name := "mapped"
		if disabled {
			name = "buffered"
		}
		t.Run(name, func(t *testing.T) {
			database, _, _ := historyReadFileFixture(t, OpenOptions{DisableHistoryMmap: disabled})
			ctx := context.Background()
			if _, err := database.db.Exec("CREATE TABLE history_mapping(payload BLOB); INSERT INTO history_mapping VALUES(zeroblob(2097152))"); err != nil {
				t.Fatal(err)
			}
			var busy, frames, done int
			if err := database.db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &done); err != nil || busy != 0 || frames != done {
				t.Fatalf("fixture checkpoint: %d %d %d %v", busy, frames, done, err)
			}
			connection, err := database.historyDB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var limit int64
			if err := connection.QueryRowContext(ctx, "PRAGMA mmap_size").Scan(&limit); err != nil {
				t.Fatal(err)
			}
			if disabled && limit != 0 || !disabled && limit != historyReadMmapBytes {
				t.Fatalf("mapping limit=%d disabled=%v", limit, disabled)
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			rows, err := database.historyDB.QueryContext(ctx, "SELECT payload FROM history_mapping")
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("mapping fixture returned no row", rows.Err())
			}
			var payload []byte
			if err := rows.Scan(&payload); err != nil || len(payload) != 2097152 {
				t.Fatalf("mapping payload length=%d error=%v", len(payload), err)
			}
			regions := historyReadMappedRegions(t, database.path)
			if disabled && regions != 0 || !disabled && regions == 0 {
				t.Fatalf("mapped regions=%d disabled=%v", regions, disabled)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if regions := historyReadMappedRegions(t, database.path); regions != 0 {
				t.Fatalf("closed Rows retained %d mappings", regions)
			}
			if stats := database.historyDB.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
				t.Fatalf("closed Rows retained reader: %+v", stats)
			}
		})
	}
}

func TestHistoryReadMappingsStayWithinBudgetForLargeFile(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("mapped reader is enabled on 64-bit Linux only")
	}
	database, _, _ := historyReadFileFixture(t, OpenOptions{})
	// The file is larger than one reader's budget. Select only the row ID so
	// the test does not allocate or copy the large payload into Go memory.
	if _, err := database.db.Exec("CREATE TABLE history_mapping_budget(payload BLOB); INSERT INTO history_mapping_budget VALUES(zeroblob(269484032))"); err != nil {
		t.Fatal(err)
	}
	var busy, frames, done int
	if err := database.db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &done); err != nil || busy != 0 || frames != done {
		t.Fatalf("fixture checkpoint: %d %d %d %v", busy, frames, done, err)
	}
	const readerBudget = uint64(256 << 20)
	info, err := os.Stat(database.path)
	if err != nil || info.Size() <= int64(readerBudget) {
		t.Fatalf("fixture must exceed reader budget: %v %v", info, err)
	}
	for i := 0; i < 2; i++ {
		rows, err := database.historyDB.QueryContext(context.Background(), "SELECT rowid FROM history_mapping_budget")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var id int64
		if !rows.Next() {
			t.Fatal("mapping fixture returned no row", rows.Err())
		}
		if err := rows.Scan(&id); err != nil || id != 1 {
			t.Fatalf("mapping row ID=%d error=%v", id, err)
		}
	}
	if stats := database.historyDB.Stats(); stats.InUse != 2 {
		t.Fatalf("both history readers must be held: %+v", stats)
	}
	raw, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		t.Fatal(err)
	}
	var mapped uint64
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasSuffix(line, " "+database.path) {
			continue
		}
		parts := strings.Fields(line)
		lo, hi, ok := strings.Cut(parts[0], "-")
		start, startErr := strconv.ParseUint(lo, 16, 64)
		end, endErr := strconv.ParseUint(hi, 16, 64)
		if !ok || startErr != nil || endErr != nil || end <= start {
			t.Fatal("invalid database mapping range")
		}
		mapped += end - start
	}
	if mapped == 0 || mapped > 2*readerBudget {
		t.Fatalf("two active readers mapped %d bytes; budget is %d", mapped, 2*readerBudget)
	}
}

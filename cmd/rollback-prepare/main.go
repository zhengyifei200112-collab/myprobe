// rollback-prepare works only on a new copy of a closed schema-013 snapshot.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/rollbackprep"
)

func main() {
	source := flag.String("source", "", "absolute path to a closed, consistent schema-013 SQLite backup")
	destination := flag.String("destination", "", "new absolute destination path; existing files are never replaced")
	expected := flag.String("expected-sha256", "", "expected complete source SHA-256 from its trusted backup record")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	result, err := rollbackprep.Prepare(ctx, *source, *destination, *expected)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rollback preparation failed:", err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}

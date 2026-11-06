// rollback-prepare works only on a new copy of a closed schema-013 snapshot.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/rollbackprep"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("rollback-prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", "", "absolute path to a closed, consistent schema-013 SQLite backup")
	destination := flags.String("destination", "", "new absolute destination path; existing files are never replaced")
	expected := flags.String("expected-sha256", "", "expected complete source SHA-256 from its trusted backup record")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	result, err := rollbackprep.Prepare(ctx, *source, *destination, *expected)
	if err != nil {
		fmt.Fprintln(stderr, "rollback preparation failed:", err)
		return 1
	}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		return 1
	}
	return 0
}

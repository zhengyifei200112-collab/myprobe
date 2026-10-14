package backup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Path identity survives normal database replacement and canonicalizes supported
// local symlinks/case aliases. SQLite databases must not be accessed via hard links.
func sourcePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", RepositoryError("invalid_database_path")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		absolute = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", RepositoryError("invalid_database_path")
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute, nil
}

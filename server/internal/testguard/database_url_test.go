package testguard_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoImplicitDefaultLieXiuDatabaseURLInGoTests keeps integration tests
// from silently targeting a developer's default database when DATABASE_URL is
// absent. The URL is assembled in pieces so this guard does not exempt itself
// from the source check.
func TestNoImplicitDefaultLieXiuDatabaseURLInGoTests(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	serverRoot := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
	forbiddenURLs := []string{
		strings.Join([]string{"postgres://", "liexiu:liexiu@localhost:5432/", "liexiu?sslmode=disable"}, ""),
		strings.Join([]string{"postgresql://", "liexiu:liexiu@localhost:5432/", "liexiu?sslmode=disable"}, ""),
	}

	var violations []string
	err := filepath.WalkDir(serverRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range forbiddenURLs {
			if strings.Contains(string(body), forbidden) {
				relative, relErr := filepath.Rel(serverRoot, path)
				if relErr != nil {
					return relErr
				}
				violations = append(violations, relative)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go tests for implicit database URLs: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("Go tests contain an implicit default database URL: %s", strings.Join(violations, ", "))
	}
}

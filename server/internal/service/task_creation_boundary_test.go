package service

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestAgentTaskCreationQueriesStayBehindKnownServices is a migration guard for
// the DIY orchestrator work. Agent task rows currently have two known owners:
// TaskService for interactive work and the orchestration service for Mission
// work. New product entry points must call those services rather than gaining
// another direct SQL creation path.
//
// As producers move behind the orchestrator, shrink this allowlist rather than
// replacing retired entrypoints with another exception.
func TestAgentTaskCreationQueriesStayBehindKnownServices(t *testing.T) {
	t.Parallel()

	allowed := map[string]map[string]struct{}{
		"server/internal/service/task.go": {
			"CreateAgentTask": {},
			"CreateRetryTask": {},
		},
		"server/internal/service/task_orchestration.go": {
			"CreateAgentTask": {},
		},
	}

	creationQuery := regexp.MustCompile(`\.(CreateAgentTask|CreateRetryTask)\s*\(`)
	repoRoot := taskCreationBoundaryRepoRoot(t)
	serverRoot := filepath.Join(repoRoot, "server")
	seen := make(map[string]int)
	var violations []string

	err := filepath.WalkDir(serverRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == filepath.Join(serverRoot, "pkg", "db", "generated") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := os.Open(path)
		if err != nil {
			return err
		}

		scanner := bufio.NewScanner(file)
		for lineNo := 1; scanner.Scan(); lineNo++ {
			matches := creationQuery.FindAllStringSubmatch(scanner.Text(), -1)
			for _, match := range matches {
				query := match[1]
				seen[query]++
				queriesForFile, fileAllowed := allowed[rel]
				_, queryAllowed := queriesForFile[query]
				if !fileAllowed || !queryAllowed {
					violations = append(violations, fmt.Sprintf("%s:%d calls %s directly", rel, lineNo, query))
				}
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			return err
		}
		return file.Close()
	})
	if err != nil {
		t.Fatalf("scan AgentTask creation boundary: %v", err)
	}

	for _, queries := range allowed {
		for query := range queries {
			if seen[query] == 0 {
				violations = append(violations, fmt.Sprintf("allowlisted query %s has no production caller; shrink the allowlist", query))
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("AgentTask creation escaped or left the known boundary:\n  %s", strings.Join(violations, "\n  "))
	}
}

// TestGenericIssueAssignmentDoesNotOwnAgentTaskDispatch locks the Wave 1B.2
// migration boundary. Ordinary Issue creation/update may keep assignee
// metadata, but must not call the execution plane; explicit Mission Commands
// and AdvanceMission own new orchestration work.
func TestGenericIssueAssignmentDoesNotOwnAgentTaskDispatch(t *testing.T) {
	t.Parallel()

	repoRoot := taskCreationBoundaryRepoRoot(t)
	for _, rel := range []string{
		"server/internal/service/issue.go",
		"server/internal/handler/issue_trigger.go",
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			// Wave 1C may delete a retired entrypoint entirely, which is stronger
			// than proving that the old producer call is absent.
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, forbidden := range []string{
			".EnqueueTaskForIssue(",
			".EnqueueTaskForIssueWithHandoff(",
		} {
			if strings.Contains(string(contents), forbidden) {
				t.Fatalf("%s still dispatches generic Issue assignment through %s", rel, forbidden)
			}
		}
	}
}

// TestQuickCreateEntrypointsUseMissionCommands locks the Wave 1B.3 migration
// boundary. The compatibility HTTP path and Slack command may keep their
// product-facing names, but neither may route to the legacy AgentTask producer.
func TestQuickCreateEntrypointsUseMissionCommands(t *testing.T) {
	t.Parallel()

	repoRoot := taskCreationBoundaryRepoRoot(t)
	checks := []struct {
		path      string
		required  string
		forbidden string
	}{
		{
			path:      "server/cmd/server/router.go",
			required:  `r.Post("/quick-create", h.QuickCreateMission)`,
			forbidden: `r.Post("/quick-create", h.QuickCreateIssue)`,
		},
	}
	for _, check := range checks {
		contents, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(check.path)))
		if err != nil {
			t.Fatalf("read %s: %v", check.path, err)
		}
		text := string(contents)
		if !strings.Contains(text, check.required) {
			t.Errorf("%s is missing Mission entrypoint %q", check.path, check.required)
		}
		if strings.Contains(text, check.forbidden) {
			t.Errorf("%s still exposes legacy quick-create producer %q", check.path, check.forbidden)
		}
	}
}

// TestWave1BLegacyEntrypointsCannotProduceAgentTasks locks the complete Wave
// 1B cutover. Compatibility metadata and read paths may remain until Wave 1C,
// but these former product entrypoints must never regain direct task dispatch.
func TestWave1BLegacyEntrypointsCannotProduceAgentTasks(t *testing.T) {
	t.Parallel()

	repoRoot := taskCreationBoundaryRepoRoot(t)
	checks := map[string][]string{
		"server/internal/service/issue.go": {
			"AssignedAgentRunFireAt",
			"AssignedTaskID",
		},
		"server/internal/handler/onboarding_shim.go": {
			".EnqueueTaskForIssue(",
		},
	}

	for rel, forbiddenTokens := range checks {
		contents, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(contents)
		for _, forbidden := range forbiddenTokens {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s still contains retired AgentTask producer token %q", rel, forbidden)
			}
		}
	}
}

func taskCreationBoundaryRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve task creation boundary test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kailonyang/liexiu/server/internal/daemon/execenv"
)

// The managed Codex cache (codex-home/.sandbox-bin) is reclaimable for
// supported Issue/QuickCreate metadata without deleting the task directory.
const sandboxBinRel = "codex-home/.sandbox-bin"

func assertGone(t *testing.T, taskDir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(taskDir, rel)); !os.IsNotExist(err) {
		t.Fatalf("%s should have been reclaimed, stat err = %v", rel, err)
	}
}

func assertKept(t *testing.T, taskDir string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(taskDir, rel)); err != nil {
			t.Fatalf("%s must be preserved: %v", rel, err)
		}
	}
}

func issueStatusMux(issueID, status string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/daemon/issues/%s/gc-check", issueID), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     status,
			"updated_at": time.Now().Add(-100 * 24 * time.Hour),
		})
	})
	return mux
}

func TestManagedArtifact_CompletedIssueReclaimsSandboxBin(t *testing.T) {
	t.Parallel()
	issueID := "aaaaaaaa-0000-0000-0000-000000000001"
	d := newGCTestDaemon(t, issueStatusMux(issueID, "in_progress"))
	meta := &execenv.GCMeta{
		Kind: execenv.GCKindIssue, IssueID: issueID, WorkspaceID: "ws",
		CompletedAt: time.Now().Add(-24 * time.Hour),
	}
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "issue", meta)
	writeFile(t, filepath.Join(taskDir, sandboxBinRel, "codex"), 4096)
	for _, rel := range []string{
		"codex-home/auth.json",
		"logs/run.log",
		"workdir/runtime/state.json",
		"workdir/repo/.sandbox-bin/user-owned",
	} {
		writeFile(t, filepath.Join(taskDir, filepath.FromSlash(rel)), 32)
	}

	action := d.shouldCleanTaskDir(context.Background(), taskDir)
	if action != gcActionCleanArtifacts {
		t.Fatalf("want artifact cleanup for old completed Issue artifact, got %d", action)
	}
	d.applyGCAction(taskDir, action, &gcStats{byPattern: map[string]int{}})
	assertGone(t, taskDir, sandboxBinRel)
	assertKept(t, taskDir,
		"codex-home/auth.json",
		"logs/run.log",
		"workdir/runtime/state.json",
		"workdir/repo/.sandbox-bin/user-owned",
		".gc_meta.json",
	)
}

func TestManagedArtifact_QuickCreateReclaimsSandboxBin(t *testing.T) {
	t.Parallel()
	taskID := "bbbbbbbb-0000-0000-0000-000000000001"
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/daemon/tasks/%s/gc-check", taskID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "running"})
	})
	d := newGCTestDaemon(t, mux)
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "quick-create", &execenv.GCMeta{
		Kind: execenv.GCKindQuickCreate, TaskID: taskID, WorkspaceID: "ws",
		CompletedAt: time.Now().Add(-24 * time.Hour),
	})
	writeFile(t, filepath.Join(taskDir, sandboxBinRel, "codex"), 4096)

	if got := d.shouldCleanTaskDir(context.Background(), taskDir); got != gcActionCleanManagedArtifacts {
		t.Fatalf("want managed artifact cleanup, got %d", got)
	}
}

func TestManagedArtifact_ArtifactTTLZeroDisablesFallback(t *testing.T) {
	t.Parallel()
	issueID := "cccccccc-0000-0000-0000-000000000001"
	d := newGCTestDaemon(t, issueStatusMux(issueID, "in_progress"))
	d.cfg.GCArtifactTTL = 0
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "no-ttl", &execenv.GCMeta{
		Kind: execenv.GCKindIssue, IssueID: issueID, WorkspaceID: "ws",
		CompletedAt: time.Now().Add(-365 * 24 * time.Hour),
	})
	writeFile(t, filepath.Join(taskDir, sandboxBinRel, "codex"), 4096)

	if got := d.shouldCleanTaskDir(context.Background(), taskDir); got != gcActionSkip {
		t.Fatalf("want skip with artifact TTL disabled, got %d", got)
	}
	assertKept(t, taskDir, sandboxBinRel+"/codex")
}

func TestManagedArtifact_ActiveEnvRootKeepsSandboxBin(t *testing.T) {
	t.Parallel()
	issueID := "dddddddd-0000-0000-0000-000000000001"
	d := newGCTestDaemon(t, issueStatusMux(issueID, "in_progress"))
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "running-issue", &execenv.GCMeta{
		Kind: execenv.GCKindIssue, IssueID: issueID, WorkspaceID: "ws",
		CompletedAt: time.Now().Add(-365 * 24 * time.Hour),
	})
	writeFile(t, filepath.Join(taskDir, sandboxBinRel, "codex"), 4096)
	d.markActiveEnvRoot(taskDir)

	if got := d.shouldCleanTaskDir(context.Background(), taskDir); got != gcActionSkip {
		t.Fatalf("want skip for active env root, got %d", got)
	}
	assertKept(t, taskDir, sandboxBinRel+"/codex")
}

func TestManagedArtifact_NeverDowngradesStrongerActions(t *testing.T) {
	t.Parallel()
	d := newGCTestDaemon(t, http.NewServeMux())
	meta := &execenv.GCMeta{
		Kind: execenv.GCKindIssue, WorkspaceID: "ws",
		CompletedAt: time.Now().Add(-365 * 24 * time.Hour),
	}
	for _, action := range []gcAction{gcActionClean, gcActionOrphan, gcActionCleanArtifacts, gcActionCleanManagedArtifacts} {
		if got := d.applyManagedArtifactFallback("dir", meta, action); got != action {
			t.Fatalf("action %d was rewritten to %d", action, got)
		}
	}
}

func TestManagedArtifact_DirectRemovalDoesNotFollowLinks(t *testing.T) {
	t.Parallel()
	d := newGCTestDaemon(t, http.NewServeMux())

	for _, tc := range []struct {
		name     string
		linkPath string
	}{
		{name: "leaf", linkPath: sandboxBinRel},
		{name: "parent", linkPath: "codex-home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskDir := t.TempDir()
			outside := t.TempDir()
			keepFile := filepath.Join(outside, "keep")
			writeFile(t, keepFile, 10)
			// Mirror the shape that makes this dangerous: the link target is
			// the user's real ~/.codex, which has a .sandbox-bin of its own.
			// Traversing the link would resolve the managed path onto it.
			userSandboxBin := filepath.Join(outside, ".sandbox-bin", "user-owned")
			writeFile(t, userSandboxBin, 10)

			linkPath := filepath.Join(taskDir, filepath.FromSlash(tc.linkPath))
			if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, linkPath); err != nil {
				t.Skipf("symlink not supported: %v", err)
			}

			removed, bytes, _ := d.cleanManagedTaskArtifacts(taskDir)
			if removed != 0 || bytes != 0 {
				t.Fatalf("removed=%d bytes=%d, want 0 through a link", removed, bytes)
			}
			for _, keep := range []string{keepFile, userSandboxBin} {
				if _, err := os.Stat(keep); err != nil {
					t.Fatalf("link target was touched: %v", err)
				}
			}
		})
	}
}

// A repository directory that merely shares the managed leaf name is not the
// managed path and must survive the direct removal.
func TestManagedArtifact_DirectRemovalIgnoresSameNamedUserDir(t *testing.T) {
	t.Parallel()
	d := newGCTestDaemon(t, http.NewServeMux())

	taskDir := t.TempDir()
	writeFile(t, filepath.Join(taskDir, "workdir/repo/.sandbox-bin/user-owned"), 32)
	writeFile(t, filepath.Join(taskDir, "codex-home/auth.json"), 32)

	if removed, _, _ := d.cleanManagedTaskArtifacts(taskDir); removed != 0 {
		t.Fatalf("removed=%d, want 0 when the managed path is absent", removed)
	}
	assertKept(t, taskDir, "workdir/repo/.sandbox-bin/user-owned", "codex-home/auth.json")
}

// When the batch issue check cannot answer (server error, scoped token), the
// task data stays but the regenerable cache is still reclaimed.
func TestManagedArtifact_UnreachableIssueStillReclaimsCache(t *testing.T) {
	t.Parallel()
	issueID := "cccccccc-0000-0000-0000-000000000001"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/daemon/workspaces/ws/issues/gc-checks", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc(fmt.Sprintf("/api/daemon/issues/%s/gc-check", issueID), func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	d := newGCTestDaemon(t, mux)

	meta := &execenv.GCMeta{
		Kind:        execenv.GCKindIssue,
		IssueID:     issueID,
		WorkspaceID: "ws",
		CompletedAt: time.Now().Add(-24 * time.Hour),
	}
	taskDir := createTaskDir(t, d.cfg.WorkspacesRoot, "ws", "unreachable-issue", meta)
	writeFile(t, filepath.Join(taskDir, sandboxBinRel, "codex"), 4096)
	writeFile(t, filepath.Join(taskDir, "output/result.md"), 32)

	stats := &gcStats{byPattern: map[string]int{}}
	d.gcWorkspaceIssues(context.Background(), "ws", []issueGCCandidate{{taskDir: taskDir, meta: meta}}, stats)

	assertGone(t, taskDir, sandboxBinRel)
	assertKept(t, taskDir, "output/result.md", ".gc_meta.json")
}

// The managed reclaim used to share cleanTaskArtifacts' tree walk and now
// addresses its paths directly. The two must agree on exactly what they remove
// — this pins that equivalence against the walk, which is still live for the
// basename patterns, so a future managed subpath or a change to either side
// cannot silently drift them apart.
func TestManagedArtifact_DirectRemovalMatchesTreeWalk(t *testing.T) {
	t.Parallel()

	fixtures := map[string]func(t *testing.T, dir string){
		"managed present": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, sandboxBinRel, "codex"), 4096)
			writeFile(t, filepath.Join(dir, "codex-home/auth.json"), 16)
		},
		"managed absent": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "codex-home/auth.json"), 16)
		},
		"user-owned same name only": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "workdir/repo/.sandbox-bin/keep"), 16)
		},
		"both managed and user-owned": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, sandboxBinRel, "codex"), 4096)
			writeFile(t, filepath.Join(dir, "workdir/repo/.sandbox-bin/keep"), 16)
		},
		"nested content and .git": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, sandboxBinRel, "a/b/c/codex"), 2048)
			writeFile(t, filepath.Join(dir, sandboxBinRel, ".git/objects/x"), 16)
			writeFile(t, filepath.Join(dir, "workdir/repo/.git/objects/y"), 16)
		},
		"codex-home is a regular file": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "codex-home"), 16)
		},
		"sandbox-bin is a regular file": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, sandboxBinRel), 16)
		},
		"leaf symlink": func(t *testing.T, dir string) {
			outside := t.TempDir()
			writeFile(t, filepath.Join(outside, ".sandbox-bin/x"), 16)
			if err := os.MkdirAll(filepath.Join(dir, "codex-home"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, sandboxBinRel)); err != nil {
				t.Skipf("symlink not supported: %v", err)
			}
		},
		"parent symlink": func(t *testing.T, dir string) {
			outside := t.TempDir()
			writeFile(t, filepath.Join(outside, ".sandbox-bin/x"), 16)
			if err := os.Symlink(outside, filepath.Join(dir, "codex-home")); err != nil {
				t.Skipf("symlink not supported: %v", err)
			}
		},
		"empty task dir": func(t *testing.T, dir string) {},
	}

	d := newGCTestDaemon(t, http.NewServeMux())
	walkMatcher := newArtifactMatcher(nil, execenv.ManagedReclaimableArtifactSubpaths())

	for name, setup := range fixtures {
		t.Run(name, func(t *testing.T) {
			walked, direct := t.TempDir(), t.TempDir()
			setup(t, walked)
			setup(t, direct)

			wRemoved, wBytes, wPattern := d.cleanTaskArtifactsMatching(walked, walkMatcher)
			dRemoved, dBytes, dPattern := d.cleanManagedTaskArtifacts(direct)

			if wRemoved != dRemoved || wBytes != dBytes || fmt.Sprint(wPattern) != fmt.Sprint(dPattern) {
				t.Fatalf("walk=(%d,%d,%v) direct=(%d,%d,%v)", wRemoved, wBytes, wPattern, dRemoved, dBytes, dPattern)
			}
			if w, dd := survivingTree(t, walked), survivingTree(t, direct); fmt.Sprint(w) != fmt.Sprint(dd) {
				t.Fatalf("survivors differ:\n walk=%v\n direct=%v", w, dd)
			}
		})
	}
}

// survivingTree lists every path left under root, relative to it, without
// following links out of the tree.
func survivingTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		out = append(out, rel)
		if e.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

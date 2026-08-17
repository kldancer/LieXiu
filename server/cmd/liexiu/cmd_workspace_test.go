package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const canonicalWorkspaceID = "11111111-1111-1111-1111-111111111111"

func newCanonicalWorkspaceTestCmd(use string) *cobra.Command {
	cmd := &cobra.Command{Use: use}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("description", "", "")
	cmd.Flags().Bool("description-stdin", false, "")
	cmd.Flags().String("context", "", "")
	cmd.Flags().Bool("context-stdin", false, "")
	cmd.Flags().String("issue-prefix", "", "")
	return cmd
}

func configureCanonicalWorkspaceTestEnv(t *testing.T, serverURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LIEXIU_SERVER_URL", serverURL)
	t.Setenv("LIEXIU_TOKEN", "mul_test_token")
	t.Setenv("LIEXIU_WORKSPACE_ID", "stale-workspace-must-not-leak")
	t.Setenv("LIEXIU_AGENT_ID", "")
	t.Setenv("LIEXIU_TASK_ID", "")
}

func canonicalWorkspaceJSON() map[string]any {
	return map[string]any{
		"id":           canonicalWorkspaceID,
		"name":         "Local Workspace",
		"slug":         "local",
		"description":  "Local workspace metadata",
		"context":      "Canonical context",
		"issue_prefix": "LOC",
	}
}

func TestWorkspaceCommandsExposeOnlyCanonicalOperations(t *testing.T) {
	for _, removed := range []string{"list", "create", "switch"} {
		for _, cmd := range workspaceCmd.Commands() {
			if cmd.Name() == removed {
				t.Fatalf("removed workspace command %q is still registered", removed)
			}
		}
	}
	for _, cmd := range workspaceMemberCmd.Commands() {
		if cmd.Name() == "invite" {
			t.Fatal("removed workspace member invite command is still registered")
		}
	}

	for _, args := range [][]string{{"get"}, {"update"}, {"member", "list"}} {
		cmd, _, err := workspaceCmd.Find(args)
		if err != nil || cmd == nil {
			t.Fatalf("canonical command %q missing: %v", strings.Join(args, " "), err)
		}
	}
	if err := workspaceGetCmd.Args(workspaceGetCmd, []string{"other-workspace"}); err == nil {
		t.Fatal("workspace get unexpectedly accepts a workspace selector")
	}
	if err := workspaceUpdateCmd.Args(workspaceUpdateCmd, []string{"other-workspace"}); err == nil {
		t.Fatal("workspace update unexpectedly accepts a workspace selector")
	}
}

func TestRunWorkspaceGetUsesCanonicalEndpointWithoutWorkspaceSelector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/workspaces/canonical" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "" {
			t.Fatalf("canonical GET sent workspace selector %q", got)
		}
		_ = json.NewEncoder(w).Encode(canonicalWorkspaceJSON())
	}))
	defer srv.Close()
	configureCanonicalWorkspaceTestEnv(t, srv.URL)

	cmd := newCanonicalWorkspaceTestCmd("get")
	out, err := captureStdout(t, func() error { return runWorkspaceGet(cmd, nil) })
	if err != nil {
		t.Fatalf("runWorkspaceGet: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode workspace output %q: %v", out, err)
	}
	if got["id"] != canonicalWorkspaceID || got["slug"] != "local" {
		t.Fatalf("workspace output = %#v", got)
	}
}

func TestRunWorkspaceUpdateResolvesCanonicalIDBeforePatch(t *testing.T) {
	var patchBody map[string]any
	var patchWorkspaceHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/workspaces/canonical":
			if r.Header.Get("X-Workspace-ID") != "" {
				t.Fatalf("canonical GET sent workspace selector")
			}
			_ = json.NewEncoder(w).Encode(canonicalWorkspaceJSON())
		case r.Method == http.MethodPatch && r.URL.Path == "/api/workspaces/"+canonicalWorkspaceID:
			patchWorkspaceHeader = r.Header.Get("X-Workspace-ID")
			if err := json.NewDecoder(r.Body).Decode(&patchBody); err != nil {
				t.Fatalf("decode patch body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(canonicalWorkspaceJSON())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	configureCanonicalWorkspaceTestEnv(t, srv.URL)

	cmd := newCanonicalWorkspaceTestCmd("update")
	if err := cmd.Flags().Set("name", "Renamed Local Workspace"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return runWorkspaceUpdate(cmd, nil) }); err != nil {
		t.Fatalf("runWorkspaceUpdate: %v", err)
	}
	if patchWorkspaceHeader != canonicalWorkspaceID {
		t.Fatalf("PATCH workspace header = %q, want canonical id", patchWorkspaceHeader)
	}
	if patchBody["name"] != "Renamed Local Workspace" {
		t.Fatalf("PATCH body = %#v", patchBody)
	}
}

func TestRunWorkspaceMemberListUsesCanonicalWorkspace(t *testing.T) {
	var gotWorkspaceHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspaces/canonical":
			if r.Header.Get("X-Workspace-ID") != "" {
				t.Fatalf("canonical GET sent workspace selector")
			}
			_ = json.NewEncoder(w).Encode(canonicalWorkspaceJSON())
		case "/api/workspaces/" + canonicalWorkspaceID + "/members":
			gotWorkspaceHeader = r.Header.Get("X-Workspace-ID")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"user_id": "owner", "name": "Owner", "email": "owner@example.com", "role": "owner"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	configureCanonicalWorkspaceTestEnv(t, srv.URL)

	cmd := newCanonicalWorkspaceTestCmd("list")
	if err := cmd.Flags().Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return runWorkspaceMembers(cmd, nil) })
	if err != nil {
		t.Fatalf("runWorkspaceMembers: %v", err)
	}
	if gotWorkspaceHeader != canonicalWorkspaceID {
		t.Fatalf("member list workspace header = %q, want canonical id", gotWorkspaceHeader)
	}
	if !strings.Contains(out, "owner@example.com") {
		t.Fatalf("member list output %q does not contain roster", out)
	}
}

func resetWorkspaceUpdateFlags(t *testing.T) {
	t.Helper()
	flags := workspaceUpdateCmd.Flags()
	for _, name := range []string{"name", "description", "context", "issue-prefix"} {
		_ = flags.Set(name, "")
		if flag := flags.Lookup(name); flag != nil {
			flag.Changed = false
		}
	}
	for _, name := range []string{"description-stdin", "context-stdin"} {
		_ = flags.Set(name, "false")
		if flag := flags.Lookup(name); flag != nil {
			flag.Changed = false
		}
	}
}

func TestBuildWorkspaceUpdateBodyRejectsEmptyIssuePrefix(t *testing.T) {
	resetWorkspaceUpdateFlags(t)
	if flag := workspaceUpdateCmd.Flags().Lookup("issue-prefix"); flag != nil {
		flag.Changed = true
	}
	_, err := buildWorkspaceUpdateBody(workspaceUpdateCmd)
	if err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("error = %v, want empty issue-prefix validation", err)
	}
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/kailonyang/liexiu/server/internal/cli"
)

func newLoginTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "login"}
	cmd.Flags().String("token", "", "")
	cmd.Flags().String("profile", "", "")
	return cmd
}

func TestResolveLoginTokenServerURLDefaultsToCloud(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LIEXIU_SERVER_URL", "")

	if got := resolveLoginTokenServerURL(newLoginTestCmd()); got != defaultCloudServerURL {
		t.Fatalf("resolveLoginTokenServerURL() = %q, want %q", got, defaultCloudServerURL)
	}
}

func TestResolveLoginTokenServerURLPrefersConfiguredServer(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LIEXIU_SERVER_URL", "")
	t.Setenv("LIEXIU_AGENT_ID", "")
	t.Setenv("LIEXIU_TASK_ID", "")
	t.Setenv("LIEXIU_DAEMON_PORT", "20032")
	cmd := newLoginTestCmd()
	if err := cmd.Flags().Set("profile", "jcode"); err != nil {
		t.Fatalf("set profile: %v", err)
	}
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{ServerURL: "https://api.example.test/"}, "jcode"); err != nil {
		t.Fatalf("SaveCLIConfig: %v", err)
	}

	if got := resolveLoginTokenServerURL(cmd); got != "https://api.example.test" {
		t.Fatalf("resolveLoginTokenServerURL() = %q, want configured server", got)
	}
}

func TestRunLoginTokenConfiguresCanonicalWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LIEXIU_TOKEN", "")
	t.Setenv("LIEXIU_WORKSPACE_ID", "")
	t.Setenv("LIEXIU_AGENT_ID", "")
	t.Setenv("LIEXIU_TASK_ID", "")
	t.Setenv(cli.TaskConfigRootEnv, "")
	t.Setenv("LIEXIU_DAEMON_PORT", "20032")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mul_test_token" {
			t.Fatalf("Authorization = %q, want bearer token", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Workspace-ID") != "" {
			t.Fatalf("login request sent stale workspace header %q", r.Header.Get("X-Workspace-ID"))
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "Ada", "email": "ada@example.com"})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/workspaces/canonical" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ws-canonical", "name": "Local Workspace"})
	}))
	defer srv.Close()
	t.Setenv("LIEXIU_SERVER_URL", srv.URL)

	cmd := newLoginTestCmd()
	if err := cmd.Flags().Set("token", "mul_test_token"); err != nil {
		t.Fatalf("set token: %v", err)
	}
	if err := cmd.Flags().Set("profile", "jcode"); err != nil {
		t.Fatalf("set profile: %v", err)
	}

	stderr := captureStderr(t)
	if err := runLogin(cmd, nil); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	errOut := stderr.read()
	if !strings.Contains(errOut, "Canonical workspace: Local Workspace (ws-canonical)") || !strings.Contains(errOut, "daemon start") {
		t.Fatalf("stderr = %q, want canonical workspace and daemon hint", errOut)
	}

	cfg, err := cli.LoadCLIConfigForProfile("jcode")
	if err != nil {
		t.Fatalf("LoadCLIConfig: %v", err)
	}
	if cfg.Token != "mul_test_token" || cfg.ServerURL != srv.URL || cfg.WorkspaceID != "ws-canonical" {
		t.Fatalf("config = %#v, want token, server URL, and canonical workspace", cfg)
	}
}

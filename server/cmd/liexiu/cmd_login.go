package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kailonyang/liexiu/server/internal/cli"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate and configure the canonical workspace",
	Long:  "Log in to LieXiu and configure the canonical workspace for this profile.",
	// Up to one positional is accepted so `--token mul_...` / `--token mcn_...`
	// (space form) can recover the token in runAuthLogin even though pflag won't
	// bind it.
	Args: cobra.MaximumNArgs(1),
	RunE: runLogin,
}

// tokenPromptSentinel is the value pflag assigns to `--token` when the flag
// is supplied without an explicit value. runAuthLoginToken treats it as
// "prompt me interactively".
const tokenPromptSentinel = "prompt"

func init() {
	loginCmd.Flags().String("token", "", "Authenticate using a personal access token (mul_... user PAT or mcn_... Cloud Node PAT). Pass --token mul_... / --token mcn_... to supply it inline, or --token alone to be prompted interactively.")
	loginCmd.Flags().Lookup("token").NoOptDefVal = tokenPromptSentinel
	loginCmd.Flags().String(callbackHostFlag, "", callbackHostFlagHelp)
}

func runLogin(cmd *cobra.Command, args []string) error {
	if err := requireHumanLocalCommand("login"); err != nil {
		return err
	}
	if err := runAuthLogin(cmd, args); err != nil {
		return err
	}
	if err := configureCanonicalWorkspace(cmd); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n→ Run 'liexiu daemon start' to start your local agent runtime.\n")
	return nil
}

func configureCanonicalWorkspace(cmd *cobra.Command) error {
	profile := resolveProfile(cmd)
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return err
	}
	if cfg.Token == "" {
		return fmt.Errorf("not authenticated")
	}
	if cfg.ServerURL == "" {
		return fmt.Errorf("server URL not configured")
	}
	// Use the profile just written by the human login flow. This avoids a
	// stale daemon-port environment overriding the authenticated profile token.
	client := cli.NewAPIClient(normalizeAPIBaseURL(cfg.ServerURL), "", cfg.Token)

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var workspace struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := client.GetJSON(ctx, "/api/workspaces/canonical", &workspace); err != nil {
		return fmt.Errorf("resolve canonical workspace after login: %w", err)
	}
	if strings.TrimSpace(workspace.ID) == "" {
		return fmt.Errorf("resolve canonical workspace after login: response did not include an id")
	}

	cfg.WorkspaceID = workspace.ID
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Canonical workspace: %s (%s)\n", workspace.Name, workspace.ID)
	return nil
}

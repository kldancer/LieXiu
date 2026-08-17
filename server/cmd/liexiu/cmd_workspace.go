package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/kailonyang/liexiu/server/internal/cli"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Inspect and update the canonical workspace",
}

var workspaceGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the canonical workspace details",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceGet,
}

var workspaceMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Inspect canonical workspace members",
}

var workspaceMemberListCmd = &cobra.Command{
	Use:   "list",
	Short: "List canonical workspace members",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceMembers,
}

var workspaceUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update canonical workspace metadata (admin/owner only)",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceUpdate,
}

func init() {
	workspaceCmd.AddCommand(workspaceGetCmd)
	workspaceCmd.AddCommand(workspaceMemberCmd)
	workspaceMemberCmd.AddCommand(workspaceMemberListCmd)
	workspaceCmd.AddCommand(workspaceUpdateCmd)

	workspaceGetCmd.Flags().String("output", "json", "Output format: json or table")
	workspaceMemberListCmd.Flags().String("output", "table", "Output format: table or json")

	workspaceUpdateCmd.Flags().String("name", "", "New workspace name")
	workspaceUpdateCmd.Flags().String("description", "", "New description (decodes \\n, \\r, \\t, \\\\; pipe via --description-stdin to preserve literal backslashes)")
	workspaceUpdateCmd.Flags().Bool("description-stdin", false, "Read description from stdin (preserves multi-line content verbatim)")
	workspaceUpdateCmd.Flags().String("context", "", "New workspace context (decodes \\n, \\r, \\t, \\\\; pipe via --context-stdin to preserve literal backslashes)")
	workspaceUpdateCmd.Flags().Bool("context-stdin", false, "Read context from stdin (preserves multi-line content verbatim)")
	workspaceUpdateCmd.Flags().String("issue-prefix", "", "New issue prefix (uppercased server-side)")
	workspaceUpdateCmd.Flags().String("output", "json", "Output format: json or table")
}

// workspaceSummary is the canonical identity needed to address the retained
// workspace-scoped metadata and roster endpoints. It is never populated from
// a collection endpoint: the server's canonical endpoint is the only source.
type workspaceSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func fetchCanonicalWorkspace(ctx context.Context, cmd *cobra.Command) (workspaceSummary, error) {
	client, err := newAPIClient(cmd)
	if err != nil {
		return workspaceSummary{}, err
	}
	client.WorkspaceID = ""

	var workspace workspaceSummary
	if err := client.GetJSON(ctx, "/api/workspaces/canonical", &workspace); err != nil {
		return workspaceSummary{}, fmt.Errorf("get canonical workspace: %w", err)
	}
	if strings.TrimSpace(workspace.ID) == "" {
		return workspaceSummary{}, fmt.Errorf("canonical workspace response did not include an id")
	}
	return workspace, nil
}

func runWorkspaceGet(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	client.WorkspaceID = ""

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	var workspace map[string]any
	if err := client.GetJSON(ctx, "/api/workspaces/canonical", &workspace); err != nil {
		return fmt.Errorf("get canonical workspace: %w", err)
	}
	return printWorkspace(cmd, workspace)
}

func runWorkspaceUpdate(cmd *cobra.Command, _ []string) error {
	body, err := buildWorkspaceUpdateBody(cmd)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("no fields to update; use --name, --description, --context, or --issue-prefix")
	}

	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	workspace, err := fetchCanonicalWorkspace(ctx, cmd)
	if err != nil {
		return err
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	client.WorkspaceID = workspace.ID

	var updated map[string]any
	if err := client.PatchJSON(ctx, "/api/workspaces/"+workspace.ID, body, &updated); err != nil {
		return fmt.Errorf("update canonical workspace: %w", err)
	}
	return printWorkspace(cmd, updated)
}

func printWorkspace(cmd *cobra.Command, workspace map[string]any) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		description := strVal(workspace, "description")
		if utf8.RuneCountInString(description) > 60 {
			runes := []rune(description)
			description = string(runes[:57]) + "..."
		}
		workspaceContext := strVal(workspace, "context")
		if utf8.RuneCountInString(workspaceContext) > 60 {
			runes := []rune(workspaceContext)
			workspaceContext = string(runes[:57]) + "..."
		}
		headers := []string{"ID", "NAME", "SLUG", "DESCRIPTION", "CONTEXT"}
		rows := [][]string{{
			strVal(workspace, "id"),
			strVal(workspace, "name"),
			strVal(workspace, "slug"),
			description,
			workspaceContext,
		}}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}

	return cli.PrintJSON(os.Stdout, workspace)
}

// buildWorkspaceUpdateBody assembles the PATCH payload from flags the caller
// actually set. Only changed flags are emitted, so omitted fields cannot be
// accidentally clobbered.
func buildWorkspaceUpdateBody(cmd *cobra.Command) (map[string]any, error) {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		v, _ := cmd.Flags().GetString("name")
		body["name"] = v
	}
	if cmd.Flags().Changed("description") || cmd.Flags().Changed("description-stdin") {
		description, _, err := resolveTextFlag(cmd, "description")
		if err != nil {
			return nil, err
		}
		body["description"] = description
	}
	if cmd.Flags().Changed("context") || cmd.Flags().Changed("context-stdin") {
		workspaceContext, _, err := resolveTextFlag(cmd, "context")
		if err != nil {
			return nil, err
		}
		body["context"] = workspaceContext
	}
	if cmd.Flags().Changed("issue-prefix") {
		v, _ := cmd.Flags().GetString("issue-prefix")
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("--issue-prefix cannot be empty; clearing the prefix is not supported")
		}
		body["issue_prefix"] = v
	}
	return body, nil
}

func runWorkspaceMembers(cmd *cobra.Command, _ []string) error {
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	workspace, err := fetchCanonicalWorkspace(ctx, cmd)
	if err != nil {
		return err
	}

	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	client.WorkspaceID = workspace.ID

	var members []map[string]any
	if err := client.GetJSON(ctx, "/api/workspaces/"+workspace.ID+"/members", &members); err != nil {
		return fmt.Errorf("list canonical workspace members: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, members)
	}

	headers := []string{"USER ID", "NAME", "EMAIL", "ROLE"}
	rows := make([][]string, 0, len(members))
	for _, member := range members {
		rows = append(rows, []string{
			strVal(member, "user_id"),
			strVal(member, "name"),
			strVal(member, "email"),
			strVal(member, "role"),
		})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}

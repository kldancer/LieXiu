package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/kailonyang/liexiu/server/internal/cli"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

var collaborateCmd = &cobra.Command{
	Use:   "collaborate",
	Short: "Use structured collaboration tools from an orchestration run",
}

var collaborateSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send one provider-neutral collaboration operation",
	Args:  cobra.NoArgs,
	RunE:  runCollaborateSend,
}

func init() {
	collaborateCmd.AddCommand(collaborateSendCmd)
	collaborateSendCmd.Flags().String("operation", "", "Operation: request_context, respond_context, send_handoff, notify_artifact, send_review_feedback, report_blocker, or request_decision")
	collaborateSendCmd.Flags().String("recipient-type", "", "Recipient type: agent or member")
	collaborateSendCmd.Flags().String("recipient-id", "", "Recipient UUID")
	collaborateSendCmd.Flags().String("dedupe-key", "", "Stable semantic dedupe key (required)")
	collaborateSendCmd.Flags().String("command-id", "", "Idempotency command UUID (generated when omitted)")
	collaborateSendCmd.Flags().String("payload", "", "Payload as one JSON object")
	collaborateSendCmd.Flags().Bool("payload-stdin", false, "Read the payload JSON object from stdin")
	collaborateSendCmd.Flags().String("payload-file", "", "Read the payload JSON object from a file inside the task workdir")
	collaborateSendCmd.Flags().Bool("allow-external-file", false, "Allow --payload-file outside the current task workdir")
	collaborateSendCmd.Flags().String("artifact-id", "", "Artifact UUID (required for notify_artifact and send_review_feedback)")
	collaborateSendCmd.Flags().String("reply-to-message-id", "", "Context request message UUID (required for respond_context)")
	collaborateSendCmd.Flags().Duration("ttl", 24*time.Hour, "Message TTL (maximum 168h)")
	collaborateSendCmd.Flags().Int("hops", 0, "Message hop count; a context response must be request hops + 1")
	collaborateSendCmd.Flags().String("output", "json", "Output format: json")
}

func runCollaborateSend(cmd *cobra.Command, _ []string) error {
	if !inAgentExecutionContext() {
		return fmt.Errorf("collaboration tools are only available inside a daemon-managed AgentTask")
	}
	operation := protocol.RuntimeCollaborationOperation(strings.TrimSpace(flagString(cmd, "operation")))
	if !operation.Valid() {
		return fmt.Errorf("--operation must be one of request_context, respond_context, send_handoff, notify_artifact, send_review_feedback, report_blocker, or request_decision")
	}
	recipientType := strings.TrimSpace(flagString(cmd, "recipient-type"))
	if recipientType != "agent" && recipientType != "member" {
		return fmt.Errorf("--recipient-type must be agent or member")
	}
	recipientID := strings.TrimSpace(flagString(cmd, "recipient-id"))
	if _, err := uuid.Parse(recipientID); err != nil {
		return fmt.Errorf("--recipient-id must be a UUID")
	}
	dedupeKey := strings.TrimSpace(flagString(cmd, "dedupe-key"))
	if dedupeKey == "" {
		return fmt.Errorf("--dedupe-key is required")
	}
	payloadText, present, err := resolveTextFlag(cmd, "payload")
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("one of --payload, --payload-stdin, or --payload-file is required")
	}
	payload, err := parseCollaborationPayload(payloadText)
	if err != nil {
		return err
	}
	commandID := strings.TrimSpace(flagString(cmd, "command-id"))
	if commandID == "" {
		commandID = uuid.NewString()
	} else if _, err := uuid.Parse(commandID); err != nil {
		return fmt.Errorf("--command-id must be a UUID")
	}
	ttl, _ := cmd.Flags().GetDuration("ttl")
	if ttl <= 0 || ttl > 7*24*time.Hour || ttl%time.Second != 0 {
		return fmt.Errorf("--ttl must be a positive whole-second duration no greater than 168h")
	}
	hops, _ := cmd.Flags().GetInt("hops")
	if hops < 0 || hops > 8 {
		return fmt.Errorf("--hops must be between 0 and 8")
	}
	artifactID := strings.TrimSpace(flagString(cmd, "artifact-id"))
	replyToMessageID := strings.TrimSpace(flagString(cmd, "reply-to-message-id"))
	if err := validateCollaborationOperationRefs(operation, artifactID, replyToMessageID, hops); err != nil {
		return err
	}
	if output := strings.TrimSpace(flagString(cmd, "output")); output != "json" {
		return fmt.Errorf("--output must be json")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	request := protocol.RuntimeCollaborationToolRequestV1{
		SchemaVersion: protocol.RuntimeCollaborationSchemaVersion,
		Operation:     operation, CommandID: commandID, DedupeKey: dedupeKey,
		Recipient:  protocol.RuntimeCollaborationRecipientV1{Type: recipientType, ID: recipientID},
		ArtifactID: artifactID, ReplyToMessageID: replyToMessageID,
		TTLSeconds: int64(ttl / time.Second), Hops: hops, Payload: payload,
	}
	var response map[string]any
	if err := client.PostJSON(ctx, "/api/orchestration/collaboration/messages", request, &response); err != nil {
		return fmt.Errorf("send collaboration operation: %w", err)
	}
	return cli.PrintJSON(cmd.OutOrStdout(), response)
}

func parseCollaborationPayload(value string) (json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	var payload map[string]json.RawMessage
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, fmt.Errorf("collaboration payload must contain exactly one JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("collaboration payload must contain exactly one JSON object")
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode collaboration payload: %w", err)
	}
	return bytes.Clone(canonical), nil
}

func validateCollaborationOperationRefs(operation protocol.RuntimeCollaborationOperation, artifactID, replyToMessageID string, hops int) error {
	needsArtifact := operation == protocol.RuntimeCollaborationNotifyArtifact || operation == protocol.RuntimeCollaborationSendReviewFeedback
	if needsArtifact {
		if _, err := uuid.Parse(artifactID); err != nil {
			return fmt.Errorf("--artifact-id must be a UUID for %s", operation)
		}
	} else if artifactID != "" {
		return fmt.Errorf("--artifact-id is only valid for notify_artifact or send_review_feedback")
	}
	if operation == protocol.RuntimeCollaborationRespondContext {
		if _, err := uuid.Parse(replyToMessageID); err != nil {
			return fmt.Errorf("--reply-to-message-id must be a UUID for respond_context")
		}
		if hops < 1 {
			return fmt.Errorf("--hops must be request hops + 1 for respond_context")
		}
	} else if replyToMessageID != "" {
		return fmt.Errorf("--reply-to-message-id is only valid for respond_context")
	}
	return nil
}

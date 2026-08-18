package orchestration

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MailboxSchemaVersion    = 1
	MailboxPayloadVersion   = 1
	MailboxMaxPayloadBytes  = 16 * 1024
	MailboxMaxPayloadFields = 64
	MailboxMaxDedupeBytes   = 128
	MailboxMaxHops          = 8
	MailboxMaxTTL           = 7 * 24 * time.Hour
)

type MailboxMessageType string

const (
	MailboxMessageContextRequest  MailboxMessageType = "context_request"
	MailboxMessageContextResponse MailboxMessageType = "context_response"
	MailboxMessageHandoff         MailboxMessageType = "handoff"
	MailboxMessageArtifactNotice  MailboxMessageType = "artifact_notice"
	MailboxMessageReviewFeedback  MailboxMessageType = "review_feedback"
	MailboxMessageBlocker         MailboxMessageType = "blocker"
	MailboxMessageDecisionRequest MailboxMessageType = "decision_request"
)

func (value MailboxMessageType) Valid() bool {
	switch value {
	case MailboxMessageContextRequest, MailboxMessageContextResponse, MailboxMessageHandoff,
		MailboxMessageArtifactNotice, MailboxMessageReviewFeedback, MailboxMessageBlocker,
		MailboxMessageDecisionRequest:
		return true
	default:
		return false
	}
}

type MailboxMessageStatus string

const (
	MailboxStatusPending   MailboxMessageStatus = "pending"
	MailboxStatusConsumed  MailboxMessageStatus = "consumed"
	MailboxStatusExpired   MailboxMessageStatus = "expired"
	MailboxStatusCancelled MailboxMessageStatus = "cancelled"
)

func (value MailboxMessageStatus) Valid() bool {
	switch value {
	case MailboxStatusPending, MailboxStatusConsumed, MailboxStatusExpired, MailboxStatusCancelled:
		return true
	default:
		return false
	}
}

type MailboxActorType string

const (
	MailboxActorMember       MailboxActorType = "member"
	MailboxActorAgent        MailboxActorType = "agent"
	MailboxActorOrchestrator MailboxActorType = "orchestrator"
)

type MailboxActorRef struct {
	Type MailboxActorType `json:"type"`
	ID   string           `json:"id,omitempty"`
}

// MailboxMessageV1 is the provider-neutral collaboration envelope. Persistence,
// authorization, reference ownership and state transitions belong to the 4C.2
// Command transaction; this type owns only the stable wire/domain shape.
type MailboxMessageV1 struct {
	SchemaVersion    int                  `json:"schema_version"`
	ID               string               `json:"id"`
	WorkspaceID      string               `json:"workspace_id"`
	MissionID        string               `json:"mission_id"`
	TaskNodeID       string               `json:"task_node_id,omitempty"`
	RunID            string               `json:"run_id,omitempty"`
	ArtifactID       string               `json:"artifact_id,omitempty"`
	ReplyToMessageID string               `json:"reply_to_message_id,omitempty"`
	Type             MailboxMessageType   `json:"type"`
	Sender           MailboxActorRef      `json:"sender"`
	Recipient        MailboxActorRef      `json:"recipient"`
	Status           MailboxMessageStatus `json:"status"`
	DedupeKey        string               `json:"dedupe_key"`
	CreatedAt        time.Time            `json:"created_at"`
	ExpiresAt        time.Time            `json:"expires_at"`
	Hops             int                  `json:"hops"`
	PayloadVersion   int                  `json:"payload_version"`
	Payload          json.RawMessage      `json:"payload"`
}

func ValidateMailboxMessageV1(message MailboxMessageV1) []ValidationError {
	var errors []ValidationError
	add := func(path, code, text string) {
		errors = append(errors, ValidationError{Path: path, Code: code, Message: text})
	}
	if message.SchemaVersion != MailboxSchemaVersion {
		add("schema_version", "unsupported_schema_version", fmt.Sprintf("schema_version must be %d", MailboxSchemaVersion))
	}
	for _, field := range []struct {
		path     string
		value    string
		required bool
	}{
		{"id", message.ID, true}, {"workspace_id", message.WorkspaceID, true}, {"mission_id", message.MissionID, true},
		{"task_node_id", message.TaskNodeID, false}, {"run_id", message.RunID, false}, {"artifact_id", message.ArtifactID, false},
		{"reply_to_message_id", message.ReplyToMessageID, false},
	} {
		if field.value == "" {
			if field.required {
				add(field.path, "missing_uuid", field.path+" is required")
			}
			continue
		}
		if _, err := uuid.Parse(field.value); err != nil {
			add(field.path, "invalid_uuid", field.path+" must be a UUID")
		}
	}
	if !message.Type.Valid() {
		add("type", "unsupported_message_type", fmt.Sprintf("message type %q is not supported", message.Type))
	}
	if !message.Status.Valid() {
		add("status", "unsupported_message_status", fmt.Sprintf("message status %q is not supported", message.Status))
	}
	validateMailboxActor(&errors, "sender", message.Sender, true)
	validateMailboxActor(&errors, "recipient", message.Recipient, false)

	dedupe := strings.TrimSpace(message.DedupeKey)
	if dedupe == "" || len(message.DedupeKey) > MailboxMaxDedupeBytes || strings.ContainsAny(message.DedupeKey, "\r\n") {
		add("dedupe_key", "invalid_dedupe_key", fmt.Sprintf("dedupe_key must contain 1 to %d single-line bytes", MailboxMaxDedupeBytes))
	}
	if message.CreatedAt.IsZero() {
		add("created_at", "missing_created_at", "created_at is required")
	}
	if message.ExpiresAt.IsZero() {
		add("expires_at", "missing_expires_at", "expires_at is required")
	} else if !message.CreatedAt.IsZero() {
		ttl := message.ExpiresAt.Sub(message.CreatedAt)
		if ttl <= 0 || ttl > MailboxMaxTTL {
			add("expires_at", "invalid_ttl", fmt.Sprintf("TTL must be positive and at most %s", MailboxMaxTTL))
		}
	}
	if message.Hops < 0 || message.Hops > MailboxMaxHops {
		add("hops", "invalid_hops", fmt.Sprintf("hops must be between 0 and %d", MailboxMaxHops))
	}
	if message.PayloadVersion != MailboxPayloadVersion {
		add("payload_version", "unsupported_payload_version", fmt.Sprintf("payload_version must be %d", MailboxPayloadVersion))
	}
	if len(message.Payload) == 0 || len(message.Payload) > MailboxMaxPayloadBytes {
		add("payload", "invalid_payload_size", fmt.Sprintf("payload must contain one JSON object of at most %d bytes", MailboxMaxPayloadBytes))
	} else {
		var payload map[string]json.RawMessage
		if err := decodeSingleJSON(message.Payload, &payload); err != nil || payload == nil {
			add("payload", "invalid_payload", "payload must contain exactly one JSON object")
		} else if len(payload) > MailboxMaxPayloadFields {
			add("payload", "too_many_payload_fields", fmt.Sprintf("payload may contain at most %d top-level fields", MailboxMaxPayloadFields))
		}
	}
	if message.Type == MailboxMessageContextResponse {
		if message.ReplyToMessageID == "" {
			add("reply_to_message_id", "missing_context_request", "context_response must reference its request")
		}
		if message.Hops == 0 {
			add("hops", "missing_response_hop", "context_response hops must be greater than zero")
		}
	} else if message.ReplyToMessageID != "" {
		add("reply_to_message_id", "unexpected_reply", "only context_response may reference another mailbox message")
	}
	if (message.Type == MailboxMessageArtifactNotice || message.Type == MailboxMessageReviewFeedback) && message.ArtifactID == "" {
		add("artifact_id", "missing_artifact", string(message.Type)+" must reference an artifact")
	}
	return errors
}

func validateMailboxActor(errors *[]ValidationError, path string, actor MailboxActorRef, sender bool) {
	add := func(code, text string) {
		*errors = append(*errors, ValidationError{Path: path, Code: code, Message: text})
	}
	switch actor.Type {
	case MailboxActorMember, MailboxActorAgent:
		if _, err := uuid.Parse(actor.ID); err != nil {
			add("invalid_actor_id", path+" member/agent id must be a UUID")
		}
	case MailboxActorOrchestrator:
		if !sender {
			add("invalid_recipient_type", "recipient must be a member or agent")
		}
		if actor.ID != "" {
			add("unexpected_actor_id", "orchestrator actor must not carry an id")
		}
	default:
		add("invalid_actor_type", path+" actor type is not supported")
	}
}

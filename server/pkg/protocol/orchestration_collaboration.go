package protocol

import "encoding/json"

// RuntimeCollaborationSchemaVersion is the provider-neutral tool contract
// exposed to every orchestration runtime. Runtime adapters may surface this
// request through a Skill, CLI, or MCP tool, but they must not change its
// meaning or let the caller assert server-owned identity fields.
const RuntimeCollaborationSchemaVersion = 1

type RuntimeCollaborationOperation string

const (
	RuntimeCollaborationRequestContext     RuntimeCollaborationOperation = "request_context"
	RuntimeCollaborationRespondContext     RuntimeCollaborationOperation = "respond_context"
	RuntimeCollaborationSendHandoff        RuntimeCollaborationOperation = "send_handoff"
	RuntimeCollaborationNotifyArtifact     RuntimeCollaborationOperation = "notify_artifact"
	RuntimeCollaborationSendReviewFeedback RuntimeCollaborationOperation = "send_review_feedback"
	RuntimeCollaborationReportBlocker      RuntimeCollaborationOperation = "report_blocker"
	RuntimeCollaborationRequestDecision    RuntimeCollaborationOperation = "request_decision"
)

func (operation RuntimeCollaborationOperation) Valid() bool {
	switch operation {
	case RuntimeCollaborationRequestContext, RuntimeCollaborationRespondContext,
		RuntimeCollaborationSendHandoff, RuntimeCollaborationNotifyArtifact,
		RuntimeCollaborationSendReviewFeedback, RuntimeCollaborationReportBlocker,
		RuntimeCollaborationRequestDecision:
		return true
	default:
		return false
	}
}

type RuntimeCollaborationRecipientV1 struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// RuntimeCollaborationToolRequestV1 deliberately omits workspace, mission,
// task-node, run, sender, and human actor identity. The server derives all of
// them from the authenticated mat_ AgentTask token. TTLSeconds=0 selects the
// server default. Payload must be exactly one bounded JSON object.
type RuntimeCollaborationToolRequestV1 struct {
	SchemaVersion    int                             `json:"schema_version"`
	Operation        RuntimeCollaborationOperation   `json:"operation"`
	CommandID        string                          `json:"command_id"`
	DedupeKey        string                          `json:"dedupe_key"`
	Recipient        RuntimeCollaborationRecipientV1 `json:"recipient"`
	ArtifactID       string                          `json:"artifact_id,omitempty"`
	ReplyToMessageID string                          `json:"reply_to_message_id,omitempty"`
	TTLSeconds       int64                           `json:"ttl_seconds,omitempty"`
	Hops             int                             `json:"hops,omitempty"`
	Payload          json.RawMessage                 `json:"payload"`
}

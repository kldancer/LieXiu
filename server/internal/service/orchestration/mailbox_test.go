package orchestration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateMailboxMessageV1AcceptsFrozenTypes(t *testing.T) {
	for _, messageType := range []MailboxMessageType{
		MailboxMessageContextRequest, MailboxMessageContextResponse, MailboxMessageHandoff,
		MailboxMessageArtifactNotice, MailboxMessageReviewFeedback, MailboxMessageBlocker,
		MailboxMessageDecisionRequest,
	} {
		t.Run(string(messageType), func(t *testing.T) {
			message := validMailboxMessageV1(messageType)
			if errors := ValidateMailboxMessageV1(message); len(errors) != 0 {
				t.Fatalf("ValidateMailboxMessageV1() errors = %#v", errors)
			}
		})
	}
}

func TestValidateMailboxMessageV1FailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*MailboxMessageV1)
		code string
	}{
		{"schema", func(m *MailboxMessageV1) { m.SchemaVersion = 2 }, "unsupported_schema_version"},
		{"mission uuid", func(m *MailboxMessageV1) { m.MissionID = "not-a-uuid" }, "invalid_uuid"},
		{"type", func(m *MailboxMessageV1) { m.Type = "chat" }, "unsupported_message_type"},
		{"status", func(m *MailboxMessageV1) { m.Status = "read" }, "unsupported_message_status"},
		{"sender id", func(m *MailboxMessageV1) { m.Sender.ID = "" }, "invalid_actor_id"},
		{"orchestrator recipient", func(m *MailboxMessageV1) { m.Recipient = MailboxActorRef{Type: MailboxActorOrchestrator} }, "invalid_recipient_type"},
		{"orchestrator sender id", func(m *MailboxMessageV1) {
			m.Sender = MailboxActorRef{Type: MailboxActorOrchestrator, ID: uuid.NewString()}
		}, "unexpected_actor_id"},
		{"dedupe", func(m *MailboxMessageV1) { m.DedupeKey = "\n" }, "invalid_dedupe_key"},
		{"ttl zero", func(m *MailboxMessageV1) { m.ExpiresAt = m.CreatedAt }, "invalid_ttl"},
		{"ttl too large", func(m *MailboxMessageV1) { m.ExpiresAt = m.CreatedAt.Add(MailboxMaxTTL + time.Second) }, "invalid_ttl"},
		{"hops", func(m *MailboxMessageV1) { m.Hops = MailboxMaxHops + 1 }, "invalid_hops"},
		{"payload version", func(m *MailboxMessageV1) { m.PayloadVersion = 2 }, "unsupported_payload_version"},
		{"payload array", func(m *MailboxMessageV1) { m.Payload = json.RawMessage(`[]`) }, "invalid_payload"},
		{"payload trailing", func(m *MailboxMessageV1) { m.Payload = json.RawMessage(`{} {}`) }, "invalid_payload"},
		{"payload too large", func(m *MailboxMessageV1) {
			m.Payload = json.RawMessage(`{"body":"` + strings.Repeat("x", MailboxMaxPayloadBytes) + `"}`)
		}, "invalid_payload_size"},
		{"response request", func(m *MailboxMessageV1) { m.ReplyToMessageID = "" }, "missing_context_request"},
		{"response hops", func(m *MailboxMessageV1) { m.Hops = 0 }, "missing_response_hop"},
		{"non-response reply", func(m *MailboxMessageV1) { m.Type = MailboxMessageHandoff }, "unexpected_reply"},
		{"artifact notice", func(m *MailboxMessageV1) { m.Type = MailboxMessageArtifactNotice; m.ArtifactID = "" }, "missing_artifact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := validMailboxMessageV1(MailboxMessageContextResponse)
			test.edit(&message)
			errors := ValidateMailboxMessageV1(message)
			if !validationErrorsContainCode(errors, test.code) {
				t.Fatalf("ValidateMailboxMessageV1() errors = %#v, want code %q", errors, test.code)
			}
		})
	}
}

func TestValidateMailboxMessageV1PayloadFieldBound(t *testing.T) {
	message := validMailboxMessageV1(MailboxMessageBlocker)
	payload := make(map[string]int, MailboxMaxPayloadFields)
	for index := 0; index < MailboxMaxPayloadFields; index++ {
		payload[string(rune('a'+index%26))+strings.Repeat("x", index/26)] = index
	}
	message.Payload, _ = json.Marshal(payload)
	if errors := ValidateMailboxMessageV1(message); len(errors) != 0 {
		t.Fatalf("maximum field payload rejected: %#v", errors)
	}
	payload["overflow"] = 1
	message.Payload, _ = json.Marshal(payload)
	if errors := ValidateMailboxMessageV1(message); !validationErrorsContainCode(errors, "too_many_payload_fields") {
		t.Fatalf("overflow payload errors = %#v", errors)
	}
}

func validMailboxMessageV1(messageType MailboxMessageType) MailboxMessageV1 {
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	message := MailboxMessageV1{
		SchemaVersion: MailboxSchemaVersion, ID: uuid.NewString(), WorkspaceID: uuid.NewString(), MissionID: uuid.NewString(),
		Type: messageType, Sender: MailboxActorRef{Type: MailboxActorAgent, ID: uuid.NewString()},
		Recipient: MailboxActorRef{Type: MailboxActorAgent, ID: uuid.NewString()}, Status: MailboxStatusPending,
		DedupeKey: "mailbox:test", CreatedAt: now, ExpiresAt: now.Add(time.Hour), PayloadVersion: MailboxPayloadVersion,
		Payload: json.RawMessage(`{"summary":"bounded collaboration fact"}`),
	}
	if messageType == MailboxMessageContextResponse {
		message.ReplyToMessageID = uuid.NewString()
		message.Hops = 1
	}
	if messageType == MailboxMessageArtifactNotice || messageType == MailboxMessageReviewFeedback {
		message.ArtifactID = uuid.NewString()
	}
	return message
}

func validationErrorsContainCode(errors []ValidationError, code string) bool {
	for _, item := range errors {
		if item.Code == code {
			return true
		}
	}
	return false
}

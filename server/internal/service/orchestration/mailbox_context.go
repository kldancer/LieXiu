package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	MailboxRunContextSchemaVersion = 1
	MailboxRunContextMaxMessages   = 16
	MailboxRunContextMaxPayload    = 64 * 1024
)

type MailboxRunMessageV1 struct {
	MessageID        string             `json:"message_id"`
	MessageRevision  int64              `json:"message_revision"`
	Type             MailboxMessageType `json:"type"`
	Sender           MailboxActorRef    `json:"sender"`
	TaskNodeID       string             `json:"task_node_id,omitempty"`
	RunID            string             `json:"run_id,omitempty"`
	ArtifactID       string             `json:"artifact_id,omitempty"`
	ReplyToMessageID string             `json:"reply_to_message_id,omitempty"`
	Hops             int                `json:"hops"`
	CreatedAt        time.Time          `json:"created_at"`
	ExpiresAt        time.Time          `json:"expires_at"`
	PayloadVersion   int                `json:"payload_version"`
	Payload          json.RawMessage    `json:"payload"`
	ContentHash      string             `json:"content_hash,omitempty"`
}

type MailboxRunContextV1 struct {
	SchemaVersion int                   `json:"schema_version"`
	Recipient     MailboxActorRef       `json:"recipient"`
	Messages      []MailboxRunMessageV1 `json:"messages"`
	ContentHash   string                `json:"content_hash,omitempty"`
}

func selectMailboxRunContext(
	ctx context.Context,
	q *db.Queries,
	workspaceID, missionID, taskNodeID, recipientAgentID pgtype.UUID,
	observedAt time.Time,
) (MailboxRunContextV1, []db.OrchestrationMailboxMessage, error) {
	rows, err := q.ListPendingMailboxMessagesForRun(ctx, db.ListPendingMailboxMessagesForRunParams{
		WorkspaceID: workspaceID, MissionID: missionID, RecipientAgentID: recipientAgentID,
		ObservedAt: timestamptz(observedAt), TaskNodeID: taskNodeID, PageSize: MailboxRunContextMaxMessages,
	})
	if err != nil {
		return MailboxRunContextV1{}, nil, fmt.Errorf("select mailbox run context: %w", err)
	}
	return buildMailboxRunContext(recipientAgentID, rows)
}

func buildMailboxRunContext(recipientAgentID pgtype.UUID, rows []db.OrchestrationMailboxMessage) (MailboxRunContextV1, []db.OrchestrationMailboxMessage, error) {
	if len(rows) == 0 {
		return MailboxRunContextV1{}, nil, nil
	}
	selected := make([]db.OrchestrationMailboxMessage, 0, len(rows))
	messages := make([]MailboxRunMessageV1, 0, len(rows))
	totalPayload := 0
	for _, row := range rows {
		stored, mapErr := mapStoredMailboxMessage(row)
		if mapErr != nil {
			return MailboxRunContextV1{}, nil, mapErr
		}
		canonicalPayload, canonicalErr := canonicalMailboxPayload(stored.Payload)
		if canonicalErr != nil {
			return MailboxRunContextV1{}, nil, canonicalErr
		}
		if totalPayload+len(canonicalPayload) > MailboxRunContextMaxPayload {
			break
		}
		message := MailboxRunMessageV1{
			MessageID: stored.ID, MessageRevision: stored.Revision, Type: stored.Type, Sender: stored.Sender,
			TaskNodeID: stored.TaskNodeID, RunID: stored.RunID, ArtifactID: stored.ArtifactID,
			ReplyToMessageID: stored.ReplyToMessageID, Hops: stored.Hops,
			CreatedAt: stored.CreatedAt, ExpiresAt: stored.ExpiresAt,
			PayloadVersion: stored.PayloadVersion, Payload: canonicalPayload,
		}
		contentHash, hashErr := hashMailboxJSON(message)
		if hashErr != nil {
			return MailboxRunContextV1{}, nil, hashErr
		}
		message.ContentHash = contentHash
		selected = append(selected, row)
		messages = append(messages, message)
		totalPayload += len(canonicalPayload)
	}
	if len(messages) == 0 {
		return MailboxRunContextV1{}, nil, nil
	}
	result := MailboxRunContextV1{
		SchemaVersion: MailboxRunContextSchemaVersion,
		Recipient:     MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(recipientAgentID)},
		Messages:      messages,
	}
	contentHash, hashErr := hashMailboxJSON(result)
	if hashErr != nil {
		return MailboxRunContextV1{}, nil, hashErr
	}
	result.ContentHash = contentHash
	return result, selected, nil
}

func attachMailboxRunContext(input []byte, mailbox MailboxRunContextV1) ([]byte, error) {
	if len(mailbox.Messages) == 0 {
		return input, nil
	}
	var object map[string]json.RawMessage
	if err := decodeSingleJSON(input, &object); err != nil || object == nil {
		return nil, fmt.Errorf("attach mailbox run context: run input must be one JSON object")
	}
	if _, exists := object["mailbox_context"]; exists {
		return nil, fmt.Errorf("attach mailbox run context: mailbox_context is already frozen")
	}
	encoded, err := json.Marshal(mailbox)
	if err != nil {
		return nil, fmt.Errorf("attach mailbox run context: %w", err)
	}
	object["mailbox_context"] = encoded
	result, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("attach mailbox run context: encode input: %w", err)
	}
	return result, nil
}

func consumeMailboxRunContext(
	ctx context.Context,
	q *db.Queries,
	mission db.Mission,
	run db.OrchestrationRun,
	recipientAgentID pgtype.UUID,
	contextSnapshot MailboxRunContextV1,
	rows []db.OrchestrationMailboxMessage,
	observedAt time.Time,
	correlationID pgtype.UUID,
) ([]db.OrchestrationActivity, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != len(contextSnapshot.Messages) {
		return nil, fmt.Errorf("consume mailbox run context: row/snapshot count mismatch")
	}
	activities := make([]db.OrchestrationActivity, 0, len(rows))
	for index, row := range rows {
		updated, err := q.TransitionMailboxMessageStatus(ctx, db.TransitionMailboxMessageStatusParams{
			TargetStatus: string(MailboxStatusConsumed), ObservedAt: timestamptz(observedAt),
			MessageID: row.ID, WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID,
			ExpectedRevision: row.Revision,
		})
		if err != nil {
			return nil, fmt.Errorf("consume mailbox run context: transition message %s: %w", uuidText(row.ID), err)
		}
		message := contextSnapshot.Messages[index]
		payload := map[string]any{
			"message_id": message.MessageID, "message_type": message.Type,
			"from_status": row.Status, "to_status": updated.Status,
			"delivery_run_id": uuidText(run.ID), "message_content_hash": message.ContentHash,
			"context_content_hash": contextSnapshot.ContentHash,
		}
		activity, err := createMailboxDeliveryActivity(ctx, q, mission, run, row, recipientAgentID, correlationID, payload)
		if err != nil {
			return nil, err
		}
		activities = append(activities, activity)
	}
	return activities, nil
}

func createMailboxDeliveryActivity(
	ctx context.Context,
	q *db.Queries,
	mission db.Mission,
	run db.OrchestrationRun,
	message db.OrchestrationMailboxMessage,
	recipientAgentID, correlationID pgtype.UUID,
	payloadValue any,
) (db.OrchestrationActivity, error) {
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return db.OrchestrationActivity{}, err
	}
	sequence, err := allocateActivitySequence(ctx, q, mission.WorkspaceID, mission.IssueID)
	if err != nil {
		return db.OrchestrationActivity{}, err
	}
	activity, err := q.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: mission.WorkspaceID, MissionID: mission.IssueID,
		TaskNodeID: message.TaskNodeID, RunID: run.ID,
		Type: activityMailboxMessageConsumed, ActorType: "agent", ActorID: recipientAgentID,
		SubjectType: activitySubjectMailboxMessage, SubjectID: message.ID,
		CausationID: run.ID, CorrelationID: correlationID,
		PayloadVersion: 1, Payload: payload,
		DedupeKey: fmt.Sprintf("mailbox:%s:consumed:run:%s", uuidText(message.ID), uuidText(run.ID)),
		Sequence:  sequence,
	})
	if err != nil {
		return db.OrchestrationActivity{}, fmt.Errorf("consume mailbox run context: create activity: %w", err)
	}
	return activity, nil
}

func canonicalMailboxPayload(payload json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := decodeSingleJSON(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("canonical mailbox payload: expected one JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonical mailbox payload: %w", err)
	}
	return canonical, nil
}

func hashMailboxJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("hash mailbox JSON: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

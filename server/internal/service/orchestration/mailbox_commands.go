package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

var (
	ErrMailboxPermissionDenied = errors.New("mailbox principal is not permitted")
	ErrMailboxReferenceInvalid = errors.New("mailbox reference does not belong to the mission")
	ErrMailboxDedupeConflict   = errors.New("mailbox dedupe key was already used for different content")
	ErrMailboxStatusConflict   = errors.New("mailbox message status or revision conflict")
	ErrMailboxExpired          = errors.New("mailbox message has expired")
)

type StoredMailboxMessage struct {
	MailboxMessageV1
	Revision        int64     `json:"revision"`
	StatusChangedAt time.Time `json:"status_changed_at"`
}

type SendMailboxMessageCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	TaskNodeID       pgtype.UUID
	RunID            pgtype.UUID
	ArtifactID       pgtype.UUID
	ReplyToMessageID pgtype.UUID
	Type             MailboxMessageType
	Sender           MailboxActorRef
	Recipient        MailboxActorRef
	DedupeKey        string
	TTL              time.Duration
	Hops             int
	PayloadVersion   int
	Payload          json.RawMessage
}

type SendMailboxMessageResult struct {
	Message    StoredMailboxMessage
	Activity   db.OrchestrationActivity
	Idempotent bool
}

type TransitionMailboxMessageCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	MessageID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	Principal        MailboxActorRef
	ActingRunID      pgtype.UUID
	ExpectedRevision int64
	TargetStatus     MailboxMessageStatus
}

type TransitionMailboxMessageResult struct {
	Message    StoredMailboxMessage
	Activity   db.OrchestrationActivity
	Idempotent bool
}

// expireMailboxMessageCommand is internal orchestration input. No API handler
// may let a caller self-assert the orchestrator principal.
type expireMailboxMessageCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	MessageID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ExpectedRevision int64
}

func (s *Service) SendMailboxMessage(ctx context.Context, command SendMailboxMessageCommand) (SendMailboxMessageResult, error) {
	if s == nil || s.repository == nil {
		return SendMailboxMessageResult{}, errors.New("send mailbox message: orchestration service is not configured")
	}
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	for _, optional := range []struct {
		path  string
		value pgtype.UUID
	}{
		{"task_node_id", command.TaskNodeID}, {"run_id", command.RunID},
		{"artifact_id", command.ArtifactID}, {"reply_to_message_id", command.ReplyToMessageID},
	} {
		if optional.value.Valid && !validUUID(optional.value) {
			errs = append(errs, ValidationError{Path: optional.path, Code: "invalid_uuid", Message: optional.path + " must be a non-zero UUID"})
		}
	}
	ttl := time.Duration(command.TTL.Microseconds()) * time.Microsecond
	if ttl <= 0 || ttl > MailboxMaxTTL {
		errs = append(errs, ValidationError{Path: "ttl", Code: "invalid_ttl", Message: "ttl must be positive and at most 168h"})
	}
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	message := MailboxMessageV1{
		SchemaVersion: MailboxSchemaVersion,
		ID:            uuid.NewString(), WorkspaceID: uuidText(command.WorkspaceID), MissionID: uuidText(command.MissionID),
		TaskNodeID: optionalUUIDText(command.TaskNodeID), RunID: optionalUUIDText(command.RunID),
		ArtifactID: optionalUUIDText(command.ArtifactID), ReplyToMessageID: optionalUUIDText(command.ReplyToMessageID),
		Type: command.Type, Sender: command.Sender, Recipient: command.Recipient,
		Status: MailboxStatusPending, DedupeKey: command.DedupeKey,
		CreatedAt: observedAt, ExpiresAt: observedAt.Add(ttl), Hops: command.Hops,
		PayloadVersion: command.PayloadVersion, Payload: command.Payload,
	}
	errs = append(errs, ValidateMailboxMessageV1(message)...)
	if len(errs) > 0 {
		return SendMailboxMessageResult{}, CommandValidationError{Errors: errs}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return SendMailboxMessageResult{}, err
	}
	message.Payload, _ = json.Marshal(payload)
	return s.repository.SendMailboxMessage(ctx, SendMailboxMessageParams{
		CommandID: command.CommandID, CorrelationID: command.CorrelationID,
		ActorID: command.ActorID, Message: message,
	})
}

func (s *Service) TransitionMailboxMessage(ctx context.Context, command TransitionMailboxMessageCommand) (TransitionMailboxMessageResult, error) {
	if s == nil || s.repository == nil {
		return TransitionMailboxMessageResult{}, errors.New("transition mailbox message: orchestration service is not configured")
	}
	errs := validateCommandIdentity(command.WorkspaceID, command.MissionID, command.CommandID, command.CorrelationID, command.ActorID, true)
	if !validUUID(command.MessageID) {
		errs = append(errs, ValidationError{Path: "message_id", Code: "invalid_uuid", Message: "message_id must be a non-zero UUID"})
	}
	if command.ActingRunID.Valid && !validUUID(command.ActingRunID) {
		errs = append(errs, ValidationError{Path: "acting_run_id", Code: "invalid_uuid", Message: "acting_run_id must be a non-zero UUID"})
	}
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if command.TargetStatus != MailboxStatusConsumed && command.TargetStatus != MailboxStatusCancelled {
		errs = append(errs, ValidationError{Path: "target_status", Code: "invalid_status_transition", Message: "member/agent command may only consume or cancel a pending message"})
	}
	if command.Principal.Type != MailboxActorMember && command.Principal.Type != MailboxActorAgent {
		errs = append(errs, ValidationError{Path: "principal", Code: "invalid_actor_type", Message: "principal must be a member or agent"})
	} else if parsed, err := uuid.Parse(command.Principal.ID); err != nil || parsed == uuid.Nil {
		errs = append(errs, ValidationError{Path: "principal.id", Code: "invalid_actor_id", Message: "principal id must be a non-zero UUID"})
	}
	if len(errs) > 0 {
		return TransitionMailboxMessageResult{}, CommandValidationError{Errors: errs}
	}
	return s.repository.TransitionMailboxMessage(ctx, TransitionMailboxMessageParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, MessageID: command.MessageID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID, ActorID: command.ActorID,
		Principal: command.Principal, ActingRunID: command.ActingRunID,
		ExpectedRevision: command.ExpectedRevision, TargetStatus: command.TargetStatus,
		ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
}

// expireMailboxMessage is reserved for the orchestrator's bounded expiry loop.
func (s *Service) expireMailboxMessage(ctx context.Context, command expireMailboxMessageCommand) (TransitionMailboxMessageResult, error) {
	if s == nil || s.repository == nil {
		return TransitionMailboxMessageResult{}, errors.New("expire mailbox message: orchestration service is not configured")
	}
	var errs []ValidationError
	for _, required := range []struct {
		path  string
		value pgtype.UUID
	}{
		{"workspace_id", command.WorkspaceID}, {"mission_id", command.MissionID},
		{"message_id", command.MessageID}, {"command_id", command.CommandID},
	} {
		if !validUUID(required.value) {
			errs = append(errs, ValidationError{Path: required.path, Code: "invalid_uuid", Message: required.path + " must be a non-zero UUID"})
		}
	}
	if command.CorrelationID.Valid && !validUUID(command.CorrelationID) {
		errs = append(errs, ValidationError{Path: "correlation_id", Code: "invalid_uuid", Message: "correlation_id must be a non-zero UUID"})
	}
	if command.ExpectedRevision < 1 {
		errs = append(errs, ValidationError{Path: "expected_revision", Code: "invalid_revision", Message: "expected_revision must be at least 1"})
	}
	if len(errs) > 0 {
		return TransitionMailboxMessageResult{}, CommandValidationError{Errors: errs}
	}
	return s.repository.expireMailboxMessage(ctx, expireMailboxMessageParams{
		WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, MessageID: command.MessageID,
		CommandID: command.CommandID, CorrelationID: command.CorrelationID,
		ExpectedRevision: command.ExpectedRevision, ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
}

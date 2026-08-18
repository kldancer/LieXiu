package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	activityMailboxMessageSent      = "mailbox.message_sent"
	activityMailboxMessageConsumed  = "mailbox.message_consumed"
	activityMailboxMessageCancelled = "mailbox.message_cancelled"
	activityMailboxMessageExpired   = "mailbox.message_expired"
	activitySubjectMailboxMessage   = "mailbox_message"
)

type SendMailboxMessageParams struct {
	CommandID     pgtype.UUID
	CorrelationID pgtype.UUID
	ActorID       pgtype.UUID
	Message       MailboxMessageV1
}

type TransitionMailboxMessageParams struct {
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
	ObservedAt       time.Time
}

type expireMailboxMessageParams struct {
	WorkspaceID, MissionID, MessageID pgtype.UUID
	CommandID, CorrelationID          pgtype.UUID
	ExpectedRevision                  int64
	ObservedAt                        time.Time
}

func (r *Repository) ListExpiredMailboxMessages(ctx context.Context, observedAt time.Time, limit int) ([]ExpiredMailboxMessage, error) {
	if r == nil || r.queries == nil {
		return nil, fmt.Errorf("list expired mailbox messages: repository is not configured")
	}
	if observedAt.IsZero() || limit <= 0 {
		return nil, fmt.Errorf("list expired mailbox messages: observed_at and positive limit are required")
	}
	rows, err := r.queries.ListExpiredMailboxMessages(ctx, db.ListExpiredMailboxMessagesParams{
		ObservedAt: pgtype.Timestamptz{Time: observedAt.UTC(), Valid: true}, PageSize: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list expired mailbox messages: %w", err)
	}
	result := make([]ExpiredMailboxMessage, 0, len(rows))
	for _, row := range rows {
		result = append(result, ExpiredMailboxMessage{
			WorkspaceID: row.WorkspaceID, MissionID: row.MissionID, MessageID: row.ID, Revision: row.Revision,
		})
	}
	return result, nil
}

func (r *Repository) ExpireMailboxMessage(ctx context.Context, params ExpireMailboxMessageParams) error {
	_, err := r.expireMailboxMessage(ctx, expireMailboxMessageParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, MessageID: params.MessageID,
		CommandID: params.CommandID, CorrelationID: params.MessageID,
		ExpectedRevision: params.Revision, ObservedAt: params.ObservedAt,
	})
	return err
}

func (r *Repository) SendMailboxMessage(ctx context.Context, params SendMailboxMessageParams) (SendMailboxMessageResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: repository is not configured")
	}
	if validationErrors := ValidateMailboxMessageV1(params.Message); len(validationErrors) > 0 {
		return SendMailboxMessageResult{}, CommandValidationError{Errors: validationErrors}
	}
	workspaceID, _ := uuidFromText(params.Message.WorkspaceID)
	missionID, _ := uuidFromText(params.Message.MissionID)
	senderID, err := mailboxActorUUID(params.Message.Sender)
	if err != nil {
		return SendMailboxMessageResult{}, err
	}
	recipientID, err := mailboxActorUUID(params.Message.Recipient)
	if err != nil {
		return SendMailboxMessageResult{}, err
	}
	messageID, _ := uuidFromText(params.Message.ID)
	taskNodeID, _ := optionalUUIDFromText(params.Message.TaskNodeID)
	runID, _ := optionalUUIDFromText(params.Message.RunID)
	artifactID, _ := optionalUUIDFromText(params.Message.ArtifactID)
	replyID, _ := optionalUUIDFromText(params.Message.ReplyToMessageID)

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	if err := qtx.LockMailboxCommand(ctx, db.LockMailboxCommandParams{WorkspaceID: workspaceID, CommandID: params.CommandID}); err != nil {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: lock command: %w", err)
	}
	if existing, lookupErr := qtx.GetMailboxMessageByCommand(ctx, db.GetMailboxMessageByCommandParams{WorkspaceID: workspaceID, CommandID: params.CommandID}); lookupErr == nil {
		return loadMailboxReplayResult(ctx, qtx, existing, params, true)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: check command: %w", lookupErr)
	}
	if _, lookupErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: workspaceID, DedupeKey: mustCommandDedupeKey(params.CommandID),
	}); lookupErr == nil {
		return SendMailboxMessageResult{}, ErrCommandConflict
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: check command activity: %w", lookupErr)
	}
	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: missionID, WorkspaceID: workspaceID})
	if err != nil {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: lock mission: %w", err)
	}
	if mission.Status == string(MissionStatusCompleted) || mission.Status == string(MissionStatusFailed) || mission.Status == string(MissionStatusCancelled) {
		return SendMailboxMessageResult{}, ErrMailboxStatusConflict
	}
	actorMember, err := qtx.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: params.ActorID, WorkspaceID: workspaceID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SendMailboxMessageResult{}, ErrMailboxPermissionDenied
		}
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: authorize actor: %w", err)
	}
	if err := validateMailboxSendPrincipal(ctx, qtx, workspaceID, missionID, runID, params.ActorID, actorMember.Role, params.Message.Sender); err != nil {
		return SendMailboxMessageResult{}, err
	}
	if err := validateMailboxRecipient(ctx, qtx, workspaceID, params.Message.Recipient); err != nil {
		return SendMailboxMessageResult{}, err
	}
	if err := validateMailboxReferences(ctx, qtx, workspaceID, missionID, taskNodeID, runID, artifactID, replyID, params.Message); err != nil {
		return SendMailboxMessageResult{}, err
	}
	if existing, lookupErr := qtx.GetMailboxMessageByDedupe(ctx, db.GetMailboxMessageByDedupeParams{
		WorkspaceID: workspaceID, MissionID: missionID, SenderType: string(params.Message.Sender.Type), SenderID: senderID, DedupeKey: params.Message.DedupeKey,
	}); lookupErr == nil {
		return loadMailboxReplayResult(ctx, qtx, existing, params, false)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: check dedupe: %w", lookupErr)
	}

	row, err := qtx.CreateMailboxMessage(ctx, db.CreateMailboxMessageParams{
		MessageID: messageID, WorkspaceID: workspaceID, MissionID: missionID,
		TaskNodeID: taskNodeID, RunID: runID, ArtifactID: artifactID, ReplyToMessageID: replyID,
		SchemaVersion: int32(params.Message.SchemaVersion), Type: string(params.Message.Type),
		SenderType: string(params.Message.Sender.Type), SenderID: senderID,
		RecipientType: string(params.Message.Recipient.Type), RecipientID: recipientID,
		DedupeKey: params.Message.DedupeKey, Hops: int32(params.Message.Hops),
		PayloadVersion: int32(params.Message.PayloadVersion), Payload: params.Message.Payload,
		CommandID: params.CommandID, CreatedBy: params.ActorID,
		CreatedAt: pgtype.Timestamptz{Time: params.Message.CreatedAt, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: params.Message.ExpiresAt, Valid: true},
	})
	if err != nil {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: insert: %w", err)
	}
	activity, err := createMailboxActivity(ctx, qtx, mailboxActivityParams{
		WorkspaceID: workspaceID, MissionID: missionID, TaskNodeID: taskNodeID, RunID: runID,
		CommandID: params.CommandID, CorrelationID: params.CorrelationID,
		Actor: params.Message.Sender, MessageID: row.ID, Type: activityMailboxMessageSent,
		Payload: mailboxActivityPayload(row, "", row.Status),
	})
	if err != nil {
		return SendMailboxMessageResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SendMailboxMessageResult{}, fmt.Errorf("send mailbox message: commit: %w", err)
	}
	mapped, err := mapStoredMailboxMessage(row)
	return SendMailboxMessageResult{Message: mapped, Activity: activity}, err
}

func (r *Repository) TransitionMailboxMessage(ctx context.Context, params TransitionMailboxMessageParams) (TransitionMailboxMessageResult, error) {
	return r.transitionMailboxMessage(ctx, params, false)
}

func (r *Repository) expireMailboxMessage(ctx context.Context, params expireMailboxMessageParams) (TransitionMailboxMessageResult, error) {
	return r.transitionMailboxMessage(ctx, TransitionMailboxMessageParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, MessageID: params.MessageID,
		CommandID: params.CommandID, CorrelationID: params.CorrelationID,
		Principal: MailboxActorRef{Type: MailboxActorOrchestrator}, ExpectedRevision: params.ExpectedRevision,
		TargetStatus: MailboxStatusExpired, ObservedAt: params.ObservedAt,
	}, true)
}

func (r *Repository) transitionMailboxMessage(ctx context.Context, params TransitionMailboxMessageParams, internalOrchestrator bool) (TransitionMailboxMessageResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: repository is not configured")
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	if err := qtx.LockMailboxCommand(ctx, db.LockMailboxCommandParams{WorkspaceID: params.WorkspaceID, CommandID: params.CommandID}); err != nil {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: lock command: %w", err)
	}
	wantActivityType := mailboxTransitionActivityType(params.TargetStatus)
	if replay, lookupErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{WorkspaceID: params.WorkspaceID, DedupeKey: mustCommandDedupeKey(params.CommandID)}); lookupErr == nil {
		if replay.SubjectType != activitySubjectMailboxMessage || replay.SubjectID != params.MessageID || replay.Type != wantActivityType {
			return TransitionMailboxMessageResult{}, ErrCommandConflict
		}
		row, loadErr := qtx.GetMailboxMessageInMission(ctx, db.GetMailboxMessageInMissionParams{MessageID: params.MessageID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
		if loadErr != nil {
			return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: load replay: %w", loadErr)
		}
		if err := authorizeMailboxTransition(ctx, qtx, row, params, internalOrchestrator); err != nil {
			return TransitionMailboxMessageResult{}, err
		}
		mapped, mapErr := mapStoredMailboxMessage(row)
		return TransitionMailboxMessageResult{Message: mapped, Activity: replay, Idempotent: true}, mapErr
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: check command: %w", lookupErr)
	}
	if _, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID}); err != nil {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: lock mission: %w", err)
	}
	row, err := qtx.LockMailboxMessageInMission(ctx, db.LockMailboxMessageInMissionParams{MessageID: params.MessageID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
	if err != nil {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: lock message: %w", err)
	}
	if row.Status != string(MailboxStatusPending) || row.Revision != params.ExpectedRevision {
		return TransitionMailboxMessageResult{}, ErrMailboxStatusConflict
	}
	if params.TargetStatus != MailboxStatusExpired && !params.ObservedAt.Before(row.ExpiresAt.Time) {
		return TransitionMailboxMessageResult{}, ErrMailboxExpired
	}
	if err := authorizeMailboxTransition(ctx, qtx, row, params, internalOrchestrator); err != nil {
		return TransitionMailboxMessageResult{}, err
	}
	updated, err := qtx.TransitionMailboxMessageStatus(ctx, db.TransitionMailboxMessageStatusParams{
		TargetStatus: string(params.TargetStatus), ObservedAt: pgtype.Timestamptz{Time: params.ObservedAt, Valid: true},
		MessageID: params.MessageID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, ExpectedRevision: params.ExpectedRevision,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TransitionMailboxMessageResult{}, ErrMailboxStatusConflict
		}
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: update: %w", err)
	}
	actor := params.Principal
	if internalOrchestrator {
		actor = MailboxActorRef{Type: MailboxActorOrchestrator}
	}
	activity, err := createMailboxActivity(ctx, qtx, mailboxActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: row.TaskNodeID, RunID: row.RunID,
		CommandID: params.CommandID, CorrelationID: params.CorrelationID, Actor: actor,
		MessageID: row.ID, Type: wantActivityType, Payload: mailboxActivityPayload(updated, row.Status, updated.Status),
	})
	if err != nil {
		return TransitionMailboxMessageResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TransitionMailboxMessageResult{}, fmt.Errorf("transition mailbox message: commit: %w", err)
	}
	mapped, err := mapStoredMailboxMessage(updated)
	return TransitionMailboxMessageResult{Message: mapped, Activity: activity}, err
}

func validateMailboxSendPrincipal(ctx context.Context, q *db.Queries, workspaceID, missionID, runID, actorID pgtype.UUID, actorRole string, sender MailboxActorRef) error {
	senderID, err := uuidFromText(sender.ID)
	switch sender.Type {
	case MailboxActorMember:
		if err != nil || senderID != actorID {
			return ErrMailboxPermissionDenied
		}
	case MailboxActorAgent:
		if actorRole != "owner" || err != nil || !runID.Valid {
			return ErrMailboxPermissionDenied
		}
		principal, loadErr := q.GetMailboxRunPrincipal(ctx, db.GetMailboxRunPrincipalParams{RunID: runID, WorkspaceID: workspaceID, MissionID: missionID})
		if loadErr != nil || principal.AgentID != senderID {
			return ErrMailboxPermissionDenied
		}
	case MailboxActorOrchestrator:
		return ErrMailboxPermissionDenied
	default:
		return ErrMailboxPermissionDenied
	}
	return nil
}

func validateMailboxRecipient(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, recipient MailboxActorRef) error {
	id, err := uuidFromText(recipient.ID)
	if err != nil {
		return ErrMailboxPermissionDenied
	}
	switch recipient.Type {
	case MailboxActorMember:
		_, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: id, WorkspaceID: workspaceID})
	case MailboxActorAgent:
		_, err = q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
	default:
		return ErrMailboxPermissionDenied
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMailboxPermissionDenied
	}
	return err
}

func validateMailboxReferences(ctx context.Context, q *db.Queries, workspaceID, missionID, taskNodeID, runID, artifactID, replyID pgtype.UUID, message MailboxMessageV1) error {
	if taskNodeID.Valid {
		if _, err := q.GetTaskNodeInMission(ctx, db.GetTaskNodeInMissionParams{TaskNodeID: taskNodeID, WorkspaceID: workspaceID, MissionID: missionID}); err != nil {
			return ErrMailboxReferenceInvalid
		}
	}
	if runID.Valid {
		run, err := q.GetMailboxRunPrincipal(ctx, db.GetMailboxRunPrincipalParams{RunID: runID, WorkspaceID: workspaceID, MissionID: missionID})
		if err != nil || (taskNodeID.Valid && run.TaskNodeID != taskNodeID) {
			return ErrMailboxReferenceInvalid
		}
	}
	if artifactID.Valid {
		artifact, err := q.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: artifactID, WorkspaceID: workspaceID})
		if err != nil || artifact.MissionID != missionID || (taskNodeID.Valid && artifact.TaskNodeID != taskNodeID) {
			return ErrMailboxReferenceInvalid
		}
		// An artifact notice proves what the sending Run produced, so it must
		// name that Run's own artifact. Review feedback necessarily points at
		// the earlier execution artifact under review; requiring the review
		// Run id to match would make the frozen review_feedback type unusable.
		if message.Type == MailboxMessageArtifactNotice && runID.Valid && artifact.RunID != runID {
			return ErrMailboxReferenceInvalid
		}
	}
	if replyID.Valid {
		reply, err := q.GetMailboxMessageInMission(ctx, db.GetMailboxMessageInMissionParams{MessageID: replyID, WorkspaceID: workspaceID, MissionID: missionID})
		if err != nil || reply.Type != string(MailboxMessageContextRequest) || message.Type != MailboxMessageContextResponse {
			return ErrMailboxReferenceInvalid
		}
		if !mailboxDBActorEqual(reply.RecipientType, reply.RecipientID, message.Sender) || !mailboxDBActorEqual(reply.SenderType, reply.SenderID, message.Recipient) || message.Hops != int(reply.Hops)+1 {
			return ErrMailboxReferenceInvalid
		}
	}
	return nil
}

func authorizeMailboxTransition(ctx context.Context, q *db.Queries, row db.OrchestrationMailboxMessage, params TransitionMailboxMessageParams, internalOrchestrator bool) error {
	if internalOrchestrator {
		if params.TargetStatus != MailboxStatusExpired || params.ObservedAt.Before(row.ExpiresAt.Time) {
			return ErrMailboxPermissionDenied
		}
		return nil
	}
	member, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: params.ActorID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return ErrMailboxPermissionDenied
	}
	principalID, err := uuidFromText(params.Principal.ID)
	if err != nil {
		return ErrMailboxPermissionDenied
	}
	if params.Principal.Type == MailboxActorMember {
		if principalID != params.ActorID {
			return ErrMailboxPermissionDenied
		}
	} else if params.Principal.Type == MailboxActorAgent {
		if member.Role != "owner" || !params.ActingRunID.Valid {
			return ErrMailboxPermissionDenied
		}
		principal, loadErr := q.GetMailboxRunPrincipal(ctx, db.GetMailboxRunPrincipalParams{RunID: params.ActingRunID, WorkspaceID: params.WorkspaceID, MissionID: params.MissionID})
		if loadErr != nil || principal.AgentID != principalID {
			return ErrMailboxPermissionDenied
		}
	} else {
		return ErrMailboxPermissionDenied
	}
	if params.TargetStatus == MailboxStatusConsumed && !mailboxDBActorEqual(row.RecipientType, row.RecipientID, params.Principal) {
		return ErrMailboxPermissionDenied
	}
	if params.TargetStatus == MailboxStatusCancelled && !mailboxDBActorEqual(row.SenderType, row.SenderID, params.Principal) {
		return ErrMailboxPermissionDenied
	}
	return nil
}

type mailboxActivityParams struct {
	WorkspaceID, MissionID, TaskNodeID, RunID pgtype.UUID
	CommandID, CorrelationID                  pgtype.UUID
	Actor                                     MailboxActorRef
	MessageID                                 pgtype.UUID
	Type                                      string
	Payload                                   []byte
}

func createMailboxActivity(ctx context.Context, q *db.Queries, params mailboxActivityParams) (db.OrchestrationActivity, error) {
	sequence, err := q.AllocateMissionActivitySequence(ctx, db.AllocateMissionActivitySequenceParams{IssueID: params.MissionID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return db.OrchestrationActivity{}, fmt.Errorf("mailbox activity: allocate sequence: %w", err)
	}
	actorID, _ := mailboxActorUUID(params.Actor)
	actorType := string(params.Actor.Type)
	if params.Actor.Type == MailboxActorMember {
		actorType = "user"
	}
	activity, err := q.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID: params.WorkspaceID, MissionID: params.MissionID, TaskNodeID: params.TaskNodeID, RunID: params.RunID,
		Type: params.Type, ActorType: actorType, ActorID: actorID,
		SubjectType: activitySubjectMailboxMessage, SubjectID: params.MessageID,
		CausationID: params.CommandID, CorrelationID: correlationOrCommand(params.CorrelationID, params.CommandID),
		PayloadVersion: 1, Payload: params.Payload, DedupeKey: mustCommandDedupeKey(params.CommandID), Sequence: sequence,
	})
	if err != nil {
		return db.OrchestrationActivity{}, fmt.Errorf("mailbox activity: insert: %w", err)
	}
	return activity, nil
}

func mailboxActivityPayload(row db.OrchestrationMailboxMessage, fromStatus, toStatus string) []byte {
	payload := map[string]any{
		"message_id": uuidText(row.ID), "message_type": row.Type,
		"recipient_type": row.RecipientType, "recipient_id": uuidText(row.RecipientID),
		"from_status": fromStatus, "to_status": toStatus, "expires_at": row.ExpiresAt.Time.UTC(), "hops": row.Hops,
	}
	encoded, _ := json.Marshal(payload)
	return encoded
}

func loadMailboxReplayResult(ctx context.Context, q *db.Queries, row db.OrchestrationMailboxMessage, params SendMailboxMessageParams, commandReplay bool) (SendMailboxMessageResult, error) {
	mapped, err := mapStoredMailboxMessage(row)
	if err != nil {
		return SendMailboxMessageResult{}, err
	}
	if row.CreatedBy != params.ActorID || !mailboxMessageMatches(mapped, params.Message) {
		if commandReplay {
			return SendMailboxMessageResult{}, ErrCommandConflict
		}
		return SendMailboxMessageResult{}, ErrMailboxDedupeConflict
	}
	member, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: params.ActorID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		return SendMailboxMessageResult{}, ErrMailboxPermissionDenied
	}
	if err := validateMailboxSendPrincipal(ctx, q, row.WorkspaceID, row.MissionID, row.RunID, params.ActorID, member.Role, mapped.Sender); err != nil {
		return SendMailboxMessageResult{}, err
	}
	activity, err := q.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: row.WorkspaceID, DedupeKey: mustCommandDedupeKey(row.CommandID),
	})
	if err != nil {
		return SendMailboxMessageResult{}, fmt.Errorf("load mailbox replay activity: %w", err)
	}
	return SendMailboxMessageResult{Message: mapped, Activity: activity, Idempotent: true}, nil
}

func mailboxMessageMatches(stored StoredMailboxMessage, requested MailboxMessageV1) bool {
	if stored.SchemaVersion != requested.SchemaVersion || stored.WorkspaceID != requested.WorkspaceID || stored.MissionID != requested.MissionID ||
		stored.TaskNodeID != requested.TaskNodeID || stored.RunID != requested.RunID || stored.ArtifactID != requested.ArtifactID ||
		stored.ReplyToMessageID != requested.ReplyToMessageID || stored.Type != requested.Type ||
		!reflect.DeepEqual(stored.Sender, requested.Sender) || !reflect.DeepEqual(stored.Recipient, requested.Recipient) ||
		stored.DedupeKey != requested.DedupeKey || stored.Hops != requested.Hops || stored.PayloadVersion != requested.PayloadVersion ||
		stored.ExpiresAt.Sub(stored.CreatedAt) != requested.ExpiresAt.Sub(requested.CreatedAt) {
		return false
	}
	var storedPayload, requestedPayload any
	return json.Unmarshal(stored.Payload, &storedPayload) == nil && json.Unmarshal(requested.Payload, &requestedPayload) == nil && reflect.DeepEqual(storedPayload, requestedPayload)
}

func mapStoredMailboxMessage(row db.OrchestrationMailboxMessage) (StoredMailboxMessage, error) {
	sender := MailboxActorRef{Type: MailboxActorType(row.SenderType)}
	if row.SenderID.Valid {
		sender.ID = uuidText(row.SenderID)
	}
	recipient := MailboxActorRef{Type: MailboxActorType(row.RecipientType), ID: uuidText(row.RecipientID)}
	message := StoredMailboxMessage{MailboxMessageV1: MailboxMessageV1{
		SchemaVersion: int(row.SchemaVersion), ID: uuidText(row.ID), WorkspaceID: uuidText(row.WorkspaceID), MissionID: uuidText(row.MissionID),
		TaskNodeID: optionalUUIDText(row.TaskNodeID), RunID: optionalUUIDText(row.RunID), ArtifactID: optionalUUIDText(row.ArtifactID),
		ReplyToMessageID: optionalUUIDText(row.ReplyToMessageID), Type: MailboxMessageType(row.Type), Sender: sender, Recipient: recipient,
		Status: MailboxMessageStatus(row.Status), DedupeKey: row.DedupeKey, CreatedAt: row.CreatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
		Hops: int(row.Hops), PayloadVersion: int(row.PayloadVersion), Payload: bytes.Clone(row.Payload),
	}, Revision: row.Revision, StatusChangedAt: row.StatusChangedAt.Time.UTC()}
	if errs := ValidateMailboxMessageV1(message.MailboxMessageV1); len(errs) > 0 {
		return StoredMailboxMessage{}, fmt.Errorf("map mailbox message: persisted row violates v1 contract: %#v", errs)
	}
	return message, nil
}

func mailboxDBActorEqual(actorType string, actorID pgtype.UUID, expected MailboxActorRef) bool {
	if actorType != string(expected.Type) {
		return false
	}
	if expected.Type == MailboxActorOrchestrator {
		return !actorID.Valid && expected.ID == ""
	}
	id, err := uuidFromText(expected.ID)
	return err == nil && actorID == id
}

func mailboxActorUUID(actor MailboxActorRef) (pgtype.UUID, error) {
	if actor.Type == MailboxActorOrchestrator && actor.ID == "" {
		return pgtype.UUID{}, nil
	}
	return uuidFromText(actor.ID)
}

func optionalUUIDFromText(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return uuidFromText(value)
}

func mailboxTransitionActivityType(status MailboxMessageStatus) string {
	switch status {
	case MailboxStatusConsumed:
		return activityMailboxMessageConsumed
	case MailboxStatusCancelled:
		return activityMailboxMessageCancelled
	case MailboxStatusExpired:
		return activityMailboxMessageExpired
	default:
		return ""
	}
}

func mustCommandDedupeKey(commandID pgtype.UUID) string {
	value, _ := commandDedupeKey(commandID)
	return value
}

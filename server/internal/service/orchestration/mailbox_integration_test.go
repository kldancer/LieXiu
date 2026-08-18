package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestMailboxPersistenceAndCommandsIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var ready bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('orchestration_mailbox_message') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		t.Skip("mailbox migration 350 is not applied")
	}

	fixture := newRoutingIntegrationFixture(t, ctx, pool)
	var memberID pgtype.UUID
	memberEmail := "mailbox-member-" + uuid.NewString() + "@liexiu.test"
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('Mailbox member',$1) RETURNING id`, memberEmail).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'member')`, fixture.workspaceID, memberID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		pool.Exec(cleanupCtx, `DELETE FROM orchestration_mailbox_message WHERE workspace_id=$1`, fixture.workspaceID)   //nolint:errcheck
		pool.Exec(cleanupCtx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, fixture.workspaceID, memberID) //nolint:errcheck
		pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id=$1`, memberID)                                               //nolint:errcheck
	})

	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	service := NewService(queries, repository, nil, DefaultPlanHardLimits())
	created, err := service.QuickCreateMission(ctx, QuickCreateMissionCommand{
		WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID, Prompt: "mailbox integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	var taskNodeID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT issue_id FROM task_node WHERE workspace_id=$1 AND mission_id=$2 AND role='executor' LIMIT 1`, fixture.workspaceID, created.MissionID).Scan(&taskNodeID); err != nil {
		t.Fatal(err)
	}
	var assignmentID, runID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO orchestration_assignment (workspace_id,mission_id,task_node_id,role,agent_id,runtime_id,status,sequence,created_by) VALUES ($1,$2,$3,'executor',$4,$5,'active',1,$6) RETURNING id`, fixture.workspaceID, created.MissionID, taskNodeID, fixture.agentID, fixture.runtimeID, fixture.ownerID).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orchestration_run (workspace_id,mission_id,task_node_id,assignment_id,purpose,attempt,status,input,dispatch_deadline_at,timeout_seconds) VALUES ($1,$2,$3,$4,'execute',1,'queued','{}',now()+interval '1 hour',3600) RETURNING id`, fixture.workspaceID, created.MissionID, taskNodeID, assignmentID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var artifactID, reviewAssignmentID, reviewRunID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO artifact (workspace_id,mission_id,task_node_id,run_id,kind,version,uri,summary,metadata) VALUES ($1,$2,$3,$4,'file',1,'repo://mailbox-artifact','','{}') RETURNING id`, fixture.workspaceID, created.MissionID, taskNodeID, runID).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orchestration_assignment (workspace_id,mission_id,task_node_id,role,agent_id,runtime_id,status,sequence,created_by) VALUES ($1,$2,$3,'reviewer',$4,$5,'active',1,$6) RETURNING id`, fixture.workspaceID, created.MissionID, taskNodeID, fixture.agentID, fixture.runtimeID, fixture.ownerID).Scan(&reviewAssignmentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orchestration_run (workspace_id,mission_id,task_node_id,assignment_id,purpose,attempt,status,input,dispatch_deadline_at,timeout_seconds) VALUES ($1,$2,$3,$4,'review',1,'queued','{}',now()+interval '1 hour',3600) RETURNING id`, fixture.workspaceID, created.MissionID, taskNodeID, reviewAssignmentID).Scan(&reviewRunID); err != nil {
		t.Fatal(err)
	}
	reviewFeedback, err := service.SendMailboxMessage(ctx, SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		TaskNodeID: taskNodeID, RunID: reviewRunID, ArtifactID: artifactID,
		Type: MailboxMessageReviewFeedback, Sender: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		Recipient: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		DedupeKey: "mailbox:cross-run-review-feedback", TTL: time.Hour,
		PayloadVersion: MailboxPayloadVersion, Payload: json.RawMessage(`{"summary":"review the earlier execution artifact"}`),
	})
	if err != nil || reviewFeedback.Message.ArtifactID != uuidText(artifactID) {
		t.Fatalf("cross-run review feedback=%#v err=%v", reviewFeedback, err)
	}
	if _, err := service.SendMailboxMessage(ctx, SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		TaskNodeID: taskNodeID, RunID: reviewRunID, ArtifactID: artifactID,
		Type: MailboxMessageArtifactNotice, Sender: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		Recipient: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		DedupeKey: "mailbox:foreign-run-artifact-notice", TTL: time.Hour,
		PayloadVersion: MailboxPayloadVersion, Payload: json.RawMessage(`{"summary":"must fail"}`),
	}); !errors.Is(err, ErrMailboxReferenceInvalid) {
		t.Fatalf("foreign-run artifact notice err=%v", err)
	}

	secretPayload := json.RawMessage(`{"question":"bounded-secret-context"}`)
	sendCommandID := newTestUUID()
	send := SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: sendCommandID,
		ActorID: fixture.ownerID, TaskNodeID: taskNodeID, RunID: runID,
		Type:      MailboxMessageContextRequest,
		Sender:    MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		Recipient: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		DedupeKey: "mailbox:context:1", TTL: time.Hour, PayloadVersion: MailboxPayloadVersion, Payload: secretPayload,
	}
	request, err := service.SendMailboxMessage(ctx, send)
	if err != nil {
		t.Fatal(err)
	}
	if request.Message.Status != MailboxStatusPending || request.Message.Revision != 1 || request.Activity.Type != activityMailboxMessageSent {
		t.Fatalf("unexpected request result: %#v", request)
	}
	replayed, err := service.SendMailboxMessage(ctx, send)
	if err != nil || !replayed.Idempotent || replayed.Message.ID != request.Message.ID {
		t.Fatalf("command replay=%#v err=%v", replayed, err)
	}
	if replayed.Activity.ID != request.Activity.ID {
		t.Fatalf("command replay activity=%v, want %v", replayed.Activity.ID, request.Activity.ID)
	}
	changed := send
	changed.Payload = json.RawMessage(`{"question":"changed"}`)
	if _, err := service.SendMailboxMessage(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("changed command replay err=%v", err)
	}
	semanticReplay := send
	semanticReplay.CommandID = newTestUUID()
	semantic, err := service.SendMailboxMessage(ctx, semanticReplay)
	if err != nil || !semantic.Idempotent || semantic.Message.ID != request.Message.ID {
		t.Fatalf("semantic replay=%#v err=%v", semantic, err)
	}
	semanticReplay.Payload = json.RawMessage(`{"question":"different"}`)
	if _, err := service.SendMailboxMessage(ctx, semanticReplay); !errors.Is(err, ErrMailboxDedupeConflict) {
		t.Fatalf("changed semantic replay err=%v", err)
	}

	concurrentCommand := send
	concurrentCommand.CommandID = newTestUUID()
	concurrentCommand.DedupeKey = "mailbox:concurrent-command"
	const concurrentWriters = 6
	type sendOutcome struct {
		result SendMailboxMessageResult
		err    error
	}
	outcomes := make(chan sendOutcome, concurrentWriters)
	var writers sync.WaitGroup
	for range concurrentWriters {
		writers.Add(1)
		go func() {
			defer writers.Done()
			result, sendErr := service.SendMailboxMessage(ctx, concurrentCommand)
			outcomes <- sendOutcome{result: result, err: sendErr}
		}()
	}
	writers.Wait()
	close(outcomes)
	var concurrentMessageID string
	createdCount := 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent send: %v", outcome.err)
		}
		if concurrentMessageID == "" {
			concurrentMessageID = outcome.result.Message.ID
		} else if outcome.result.Message.ID != concurrentMessageID {
			t.Fatalf("concurrent command created multiple messages: %s and %s", concurrentMessageID, outcome.result.Message.ID)
		}
		if !outcome.result.Idempotent {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent command non-idempotent results=%d, want 1", createdCount)
	}

	responseCommand := SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		TaskNodeID: taskNodeID, RunID: runID, ReplyToMessageID: uuidPG(request.Message.ID),
		Type:      MailboxMessageContextResponse,
		Sender:    MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		Recipient: MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		DedupeKey: "mailbox:context-response:1", TTL: time.Hour, Hops: 1,
		PayloadVersion: MailboxPayloadVersion, Payload: json.RawMessage(`{"answer":"bounded"}`),
	}
	response, err := service.SendMailboxMessage(ctx, responseCommand)
	if err != nil {
		t.Fatal(err)
	}
	wrongResponse := responseCommand
	wrongResponse.CommandID = newTestUUID()
	wrongResponse.DedupeKey = "mailbox:wrong-response"
	wrongResponse.Recipient.ID = uuidText(memberID)
	if _, err := service.SendMailboxMessage(ctx, wrongResponse); !errors.Is(err, ErrMailboxReferenceInvalid) {
		t.Fatalf("wrong response lineage err=%v", err)
	}

	consumeCommand := TransitionMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, MessageID: uuidPG(request.Message.ID),
		CommandID: newTestUUID(), ActorID: fixture.ownerID,
		Principal: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)}, ActingRunID: runID,
		ExpectedRevision: 1, TargetStatus: MailboxStatusConsumed,
	}
	consumed, err := service.TransitionMailboxMessage(ctx, consumeCommand)
	if err != nil || consumed.Message.Status != MailboxStatusConsumed || consumed.Message.Revision != 2 {
		t.Fatalf("consume=%#v err=%v", consumed, err)
	}
	consumeReplay, err := service.TransitionMailboxMessage(ctx, consumeCommand)
	if err != nil || !consumeReplay.Idempotent || consumeReplay.Message.Status != MailboxStatusConsumed {
		t.Fatalf("consume replay=%#v err=%v", consumeReplay, err)
	}

	cancelled, err := service.TransitionMailboxMessage(ctx, TransitionMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, MessageID: uuidPG(response.Message.ID),
		CommandID: newTestUUID(), ActorID: fixture.ownerID,
		Principal: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)}, ActingRunID: runID,
		ExpectedRevision: 1, TargetStatus: MailboxStatusCancelled,
	})
	if err != nil || cancelled.Message.Status != MailboxStatusCancelled {
		t.Fatalf("cancel=%#v err=%v", cancelled, err)
	}

	memberMessage, err := service.SendMailboxMessage(ctx, SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		Type: MailboxMessageBlocker, Sender: MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		Recipient: MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		DedupeKey: "mailbox:owner-only", TTL: time.Hour, PayloadVersion: 1, Payload: json.RawMessage(`{"summary":"owner"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionMailboxMessage(ctx, TransitionMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, MessageID: uuidPG(memberMessage.Message.ID),
		CommandID: newTestUUID(), ActorID: memberID, Principal: MailboxActorRef{Type: MailboxActorMember, ID: uuidText(memberID)},
		ExpectedRevision: 1, TargetStatus: MailboxStatusConsumed,
	}); !errors.Is(err, ErrMailboxPermissionDenied) {
		t.Fatalf("unauthorized consume err=%v", err)
	}
	ownerConsumeCommand := TransitionMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, MessageID: uuidPG(memberMessage.Message.ID),
		CommandID: newTestUUID(), ActorID: fixture.ownerID,
		Principal:        MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		ExpectedRevision: 1, TargetStatus: MailboxStatusConsumed,
	}
	if _, err := service.TransitionMailboxMessage(ctx, ownerConsumeCommand); err != nil {
		t.Fatalf("owner consume: %v", err)
	}
	unauthorizedReplay := ownerConsumeCommand
	unauthorizedReplay.ActorID = memberID
	unauthorizedReplay.Principal.ID = uuidText(memberID)
	if _, err := service.TransitionMailboxMessage(ctx, unauthorizedReplay); !errors.Is(err, ErrMailboxPermissionDenied) {
		t.Fatalf("unauthorized command replay err=%v", err)
	}

	expiring, err := service.SendMailboxMessage(ctx, SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		Type: MailboxMessageDecisionRequest, Sender: MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		Recipient: MailboxActorRef{Type: MailboxActorMember, ID: uuidText(memberID)},
		DedupeKey: "mailbox:expiring", TTL: time.Millisecond, PayloadVersion: 1, Payload: json.RawMessage(`{"decision":"needed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	reconciler := NewRunReconciler(repository, nil, RunReconcilerOptions{BatchSize: 8})
	processed, err := reconciler.expireMailboxBatch(ctx)
	if err != nil || processed != 1 {
		t.Fatalf("background expiry processed=%d err=%v", processed, err)
	}
	var expiredStatus, expiredActorType string
	if err := pool.QueryRow(ctx, `
		SELECT message.status, activity.actor_type
		FROM orchestration_mailbox_message message
		JOIN orchestration_activity activity ON activity.subject_type='mailbox_message' AND activity.subject_id=message.id AND activity.type='mailbox.message_expired'
		WHERE message.id=$1`, uuidPG(expiring.Message.ID)).Scan(&expiredStatus, &expiredActorType); err != nil {
		t.Fatal(err)
	}
	if expiredStatus != string(MailboxStatusExpired) || expiredActorType != "orchestrator" {
		t.Fatalf("expired status=%q actor=%q", expiredStatus, expiredActorType)
	}
	if replayedCount, replayErr := reconciler.expireMailboxBatch(ctx); replayErr != nil || replayedCount != 0 {
		t.Fatalf("background expiry replay processed=%d err=%v", replayedCount, replayErr)
	}

	var missionRevision int64
	if err := pool.QueryRow(ctx, `SELECT revision FROM mission WHERE issue_id=$1`, created.MissionID).Scan(&missionRevision); err != nil {
		t.Fatal(err)
	}
	if missionRevision != created.Revision {
		t.Fatalf("mailbox changed mission revision: got %d want %d", missionRevision, created.Revision)
	}
	var payloadLeakCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_activity WHERE workspace_id=$1 AND mission_id=$2 AND payload::text LIKE '%bounded-secret-context%'`, fixture.workspaceID, created.MissionID).Scan(&payloadLeakCount); err != nil {
		t.Fatal(err)
	}
	if payloadLeakCount != 0 {
		t.Fatalf("activity copied mailbox payload content")
	}
}

func uuidPG(value string) pgtype.UUID {
	parsed := uuid.MustParse(value)
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

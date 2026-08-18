package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestMailboxRunContextAdvanceAndRetryIntegration(t *testing.T) {
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
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM orchestration_mailbox_message WHERE workspace_id=$1`, fixture.workspaceID) //nolint:errcheck
	})
	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	service := NewService(queries, repository, nil, DefaultPlanHardLimits())
	created, err := service.QuickCreateMission(ctx, QuickCreateMissionCommand{
		WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID, Prompt: "mailbox context",
	})
	if err != nil {
		t.Fatal(err)
	}
	var executeNodeID, integrateNodeID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT issue_id FROM task_node WHERE mission_id=$1 AND node_key='execute'`, created.MissionID).Scan(&executeNodeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT issue_id FROM task_node WHERE mission_id=$1 AND node_key='integrate'`, created.MissionID).Scan(&integrateNodeID); err != nil {
		t.Fatal(err)
	}

	eligibleIDs := make([]string, 0, MailboxRunContextMaxMessages)
	for index := 0; index < MailboxRunContextMaxMessages; index++ {
		taskNodeID := pgtype.UUID{}
		if index == 1 {
			taskNodeID = executeNodeID
		}
		message := sendMailboxContextTestMessage(t, ctx, service, fixture, created.MissionID, taskNodeID,
			"eligible-"+string(rune('a'+index)), time.Hour)
		eligibleIDs = append(eligibleIDs, message.Message.ID)
	}
	wrongTask := sendMailboxContextTestMessage(t, ctx, service, fixture, created.MissionID, integrateNodeID, "wrong-task", time.Hour)
	overflowOne := sendMailboxContextTestMessage(t, ctx, service, fixture, created.MissionID, pgtype.UUID{}, "overflow-one", time.Hour)
	overflowTwo := sendMailboxContextTestMessage(t, ctx, service, fixture, created.MissionID, pgtype.UUID{}, "overflow-two", time.Hour)
	expired := sendMailboxContextTestMessage(t, ctx, service, fixture, created.MissionID, pgtype.UUID{}, "expired", time.Millisecond)

	bindings := seedRolePolicyBindings(t, ctx, repository, fixture.workspaceID, fixture.ownerID, DutyExecutor, DutyReviewer, DutyIntegrator)
	for index := range bindings {
		bindings[index].AgentID = fixture.agentID
	}
	if _, err := service.StartMission(ctx, StartMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		ExpectedRevision: created.Revision, RolePolicyBindings: bindings,
	}); err != nil {
		t.Fatal(err)
	}
	var revisionBefore int64
	if err := pool.QueryRow(ctx, `SELECT revision FROM mission WHERE issue_id=$1`, created.MissionID).Scan(&revisionBefore); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Now().UTC().Add(10 * time.Millisecond)
	advanced, err := repository.AdvanceMission(ctx, AdvanceMissionParams{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(),
		ObservedAt: observedAt, DispatchWindow: time.Minute, RunTimeout: time.Minute,
	})
	if err != nil || len(advanced.CreatedRuns) != 1 {
		t.Fatalf("advance runs=%#v err=%v", advanced.CreatedRuns, err)
	}
	firstRun := advanced.CreatedRuns[0]
	var frozen struct {
		MailboxContext MailboxRunContextV1 `json:"mailbox_context"`
	}
	if err := json.Unmarshal(firstRun.Input, &frozen); err != nil {
		t.Fatal(err)
	}
	if len(frozen.MailboxContext.Messages) != MailboxRunContextMaxMessages || frozen.MailboxContext.Recipient.ID != uuidText(fixture.agentID) {
		t.Fatalf("mailbox context=%#v", frozen.MailboxContext)
	}
	for index, message := range frozen.MailboxContext.Messages {
		if message.MessageID != eligibleIDs[index] {
			t.Fatalf("message[%d]=%s want=%s", index, message.MessageID, eligibleIDs[index])
		}
		want := message.ContentHash
		message.ContentHash = ""
		if got, hashErr := hashMailboxJSON(message); hashErr != nil || got != want {
			t.Fatalf("message[%d] hash=%s want=%s err=%v", index, got, want, hashErr)
		}
	}
	wantContextHash := frozen.MailboxContext.ContentHash
	frozen.MailboxContext.ContentHash = ""
	if got, hashErr := hashMailboxJSON(frozen.MailboxContext); hashErr != nil || got != wantContextHash {
		t.Fatalf("context hash=%s want=%s err=%v", got, wantContextHash, hashErr)
	}

	var consumedCount, pendingExcluded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_mailbox_message WHERE workspace_id=$1 AND mission_id=$2 AND status='consumed'`, fixture.workspaceID, created.MissionID).Scan(&consumedCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_mailbox_message WHERE id=ANY($1::uuid[]) AND status='pending'`, []string{wrongTask.Message.ID, overflowOne.Message.ID, overflowTwo.Message.ID, expired.Message.ID}).Scan(&pendingExcluded); err != nil {
		t.Fatal(err)
	}
	if consumedCount != MailboxRunContextMaxMessages || pendingExcluded != 4 {
		t.Fatalf("consumed=%d pending excluded=%d", consumedCount, pendingExcluded)
	}
	var consumedActivities, payloadLeaks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_activity WHERE workspace_id=$1 AND mission_id=$2 AND type=$3 AND run_id=$4`, fixture.workspaceID, created.MissionID, activityMailboxMessageConsumed, firstRun.ID).Scan(&consumedActivities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_activity WHERE workspace_id=$1 AND mission_id=$2 AND type=$3 AND payload::text LIKE '%eligible-%'`, fixture.workspaceID, created.MissionID, activityMailboxMessageConsumed).Scan(&payloadLeaks); err != nil {
		t.Fatal(err)
	}
	if consumedActivities != MailboxRunContextMaxMessages || payloadLeaks != 0 {
		t.Fatalf("consumed activities=%d payload leaks=%d", consumedActivities, payloadLeaks)
	}
	var revisionAfter int64
	if err := pool.QueryRow(ctx, `SELECT revision FROM mission WHERE issue_id=$1`, created.MissionID).Scan(&revisionAfter); err != nil {
		t.Fatal(err)
	}
	if revisionAfter != revisionBefore {
		t.Fatalf("mailbox delivery changed mission revision: %d -> %d", revisionBefore, revisionAfter)
	}

	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET status='failed',failure_kind=$2,failure_message='retry',finished_at=$3 WHERE id=$1`, firstRun.ID, FailureKindProviderNetwork, observedAt); err != nil {
		t.Fatal(err)
	}
	retried, err := repository.AdvanceMission(ctx, AdvanceMissionParams{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(),
		ObservedAt: observedAt.Add(time.Second), DispatchWindow: time.Minute, RunTimeout: time.Minute,
	})
	if err != nil || len(retried.CreatedRuns) != 1 {
		t.Fatalf("retry runs=%#v err=%v", retried.CreatedRuns, err)
	}
	if !bytes.Equal(retried.CreatedRuns[0].Input, firstRun.Input) || retried.CreatedRuns[0].RetryOfID != firstRun.ID {
		t.Fatalf("retry input or lineage changed")
	}
	var consumedAfterRetry int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orchestration_mailbox_message WHERE workspace_id=$1 AND mission_id=$2 AND status='consumed'`, fixture.workspaceID, created.MissionID).Scan(&consumedAfterRetry); err != nil {
		t.Fatal(err)
	}
	if consumedAfterRetry != consumedCount {
		t.Fatalf("retry consumed new mailbox messages: before=%d after=%d", consumedCount, consumedAfterRetry)
	}
}

func TestMailboxPlanningRunContextIntegration(t *testing.T) {
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
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM orchestration_mailbox_message WHERE workspace_id=$1`, fixture.workspaceID) //nolint:errcheck
	})
	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	gateway := &recordingPlanGateway{}
	service := NewService(queries, repository, gateway, DefaultPlanHardLimits())
	created, err := service.CreateMission(ctx, CreateMissionCommand{
		WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		Title: "planning mailbox", Limits: PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	message := sendMailboxContextTestMessage(t, ctx, service, fixture, created.Mission.IssueID, pgtype.UUID{}, "planner-context", time.Hour)
	plannerBinding := rolePolicyBindingFor(t, seedRolePolicyBindings(t, ctx, repository, fixture.workspaceID, fixture.ownerID, DutyPlanner), DutyPlanner)
	plannerBinding.AgentID = fixture.agentID
	planned, err := service.RequestPlan(ctx, RequestPlanCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.Mission.IssueID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		ExpectedRevision: 1, RolePolicyBinding: plannerBinding,
		Input: PlanProposalInput{Objective: "use mailbox", DeliveryCriteria: []string{"bounded"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		MailboxContext MailboxRunContextV1 `json:"mailbox_context"`
	}
	if err := json.Unmarshal(planned.Run.Input, &frozen); err != nil || len(frozen.MailboxContext.Messages) != 1 || frozen.MailboxContext.Messages[0].MessageID != message.Message.ID {
		t.Fatalf("planning mailbox context=%#v err=%v", frozen.MailboxContext, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM orchestration_mailbox_message WHERE id=$1`, uuidPG(message.Message.ID)).Scan(&status); err != nil || status != string(MailboxStatusConsumed) {
		t.Fatalf("planning message status=%q err=%v", status, err)
	}
}

func TestMailboxReviewRunContextIntegration(t *testing.T) {
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
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM orchestration_mailbox_message WHERE workspace_id=$1`, fixture.workspaceID) //nolint:errcheck
	})
	_, reviewerAgentID := addIndependentAgent(t, ctx, pool, fixture)
	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	service := NewService(queries, repository, nil, DefaultPlanHardLimits())
	created, err := service.QuickCreateMission(ctx, QuickCreateMissionCommand{
		WorkspaceID: fixture.workspaceID, CommandID: newTestUUID(), ActorID: fixture.ownerID, Prompt: "review mailbox context",
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings := seedRolePolicyBindings(t, ctx, repository, fixture.workspaceID, fixture.ownerID, DutyExecutor, DutyReviewer, DutyIntegrator)
	for index := range bindings {
		bindings[index].AgentID = fixture.agentID
		if bindings[index].Duty == DutyReviewer {
			bindings[index].AgentID = reviewerAgentID
		}
	}
	if _, err := service.StartMission(ctx, StartMissionCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		ExpectedRevision: created.Revision, RolePolicyBindings: bindings,
	}); err != nil {
		t.Fatal(err)
	}
	work, err := repository.AdvanceMission(ctx, AdvanceMissionParams{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(),
		ObservedAt: time.Now().UTC(), DispatchWindow: time.Minute, RunTimeout: time.Minute,
	})
	if err != nil || len(work.CreatedRuns) != 1 {
		t.Fatalf("work advance=%#v err=%v", work.CreatedRuns, err)
	}
	workRun := work.CreatedRuns[0]
	var nodeID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT issue_id FROM task_node WHERE mission_id=$1 AND node_key='execute'`, created.MissionID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orchestration_run SET status='succeeded',finished_at=now() WHERE id=$1`, workRun.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE task_node SET status='review',revision=revision+1,updated_at=now() WHERE issue_id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	var artifactID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO artifact (workspace_id,mission_id,task_node_id,run_id,kind,version,uri,summary,metadata) VALUES ($1,$2,$3,$4,'file',1,'repo://review-context','','{}') RETURNING id`, fixture.workspaceID, created.MissionID, nodeID, workRun.ID).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	message, err := service.SendMailboxMessage(ctx, SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		TaskNodeID: nodeID, ArtifactID: artifactID, Type: MailboxMessageReviewFeedback,
		Sender:    MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		Recipient: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(reviewerAgentID)},
		DedupeKey: "review-context:" + uuid.NewString(), TTL: time.Hour,
		PayloadVersion: 1, Payload: json.RawMessage(`{"focus":"artifact evidence"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := repository.AdvanceMission(ctx, AdvanceMissionParams{
		WorkspaceID: fixture.workspaceID, MissionID: created.MissionID, CorrelationID: newTestUUID(),
		ObservedAt: time.Now().UTC(), DispatchWindow: time.Minute, RunTimeout: time.Minute,
	})
	if err != nil || len(reviewed.CreatedRuns) != 1 || reviewed.CreatedRuns[0].Purpose != "review" {
		t.Fatalf("review advance=%#v err=%v", reviewed.CreatedRuns, err)
	}
	var frozen struct {
		ArtifactID     string              `json:"artifact_id"`
		MailboxContext MailboxRunContextV1 `json:"mailbox_context"`
	}
	if err := json.Unmarshal(reviewed.CreatedRuns[0].Input, &frozen); err != nil || frozen.ArtifactID != uuidText(artifactID) || len(frozen.MailboxContext.Messages) != 1 || frozen.MailboxContext.Messages[0].MessageID != message.Message.ID {
		t.Fatalf("review input=%s frozen=%#v err=%v", reviewed.CreatedRuns[0].Input, frozen, err)
	}
}

func sendMailboxContextTestMessage(
	t *testing.T,
	ctx context.Context,
	service *Service,
	fixture routingIntegrationFixture,
	missionID, taskNodeID pgtype.UUID,
	key string,
	ttl time.Duration,
) SendMailboxMessageResult {
	t.Helper()
	result, err := service.SendMailboxMessage(ctx, SendMailboxMessageCommand{
		WorkspaceID: fixture.workspaceID, MissionID: missionID, CommandID: newTestUUID(), ActorID: fixture.ownerID,
		TaskNodeID: taskNodeID, Type: MailboxMessageHandoff,
		Sender:    MailboxActorRef{Type: MailboxActorMember, ID: uuidText(fixture.ownerID)},
		Recipient: MailboxActorRef{Type: MailboxActorAgent, ID: uuidText(fixture.agentID)},
		DedupeKey: "run-context:" + key + ":" + uuid.NewString(), TTL: ttl,
		PayloadVersion: 1, Payload: json.RawMessage(`{"summary":"` + key + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

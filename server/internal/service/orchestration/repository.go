package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/issueposition"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	activityMissionCreated      = "mission.created"
	activityMissionPlanAccepted = "mission.plan_accepted"
	activitySubjectMission      = "mission"
)

var (
	ErrCommandConflict  = errors.New("command id was already used for another command")
	ErrMissionNotDraft  = errors.New("mission is not draft")
	ErrRevisionConflict = errors.New("mission revision conflict")
)

// TxStarter is the transaction capability required by Repository.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repository persists orchestration aggregates without exposing sqlc details
// to the command service. Each exported write is one database transaction.
type Repository struct {
	queries   *db.Queries
	txStarter TxStarter
}

func NewRepository(queries *db.Queries, txStarter TxStarter) *Repository {
	return &Repository{queries: queries, txStarter: txStarter}
}

type CreateMissionParams struct {
	WorkspaceID   pgtype.UUID
	CommandID     pgtype.UUID
	CorrelationID pgtype.UUID
	ActorID       pgtype.UUID
	Title         string
	Description   pgtype.Text
	ProjectID     pgtype.UUID
	Limits        PlanLimits
}

type CreateMissionResult struct {
	Issue      db.Issue
	Mission    db.Mission
	Activity   db.OrchestrationActivity
	Idempotent bool
}

type SubmitPlanParams struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorType        string
	ActorID          pgtype.UUID
	ExpectedRevision int64
	Plan             Plan
	SourceArtifactID pgtype.UUID
	PlanSource       PlanSource
}

type SubmitPlanResult struct {
	Mission      db.Mission
	TaskNodes    []db.TaskNode
	Dependencies []db.IssueDependency
	Activity     db.OrchestrationActivity
	Idempotent   bool
}

func (r *Repository) CreateMission(ctx context.Context, params CreateMissionParams) (CreateMissionResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: params.WorkspaceID,
		DedupeKey:   dedupeKey,
	})
	if err == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return CreateMissionResult{}, fmt.Errorf("create mission: rollback idempotent transaction: %w", rollbackErr)
		}
		return r.loadCreateMissionResult(ctx, params.WorkspaceID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CreateMissionResult{}, fmt.Errorf("create mission: check command: %w", err)
	}

	limitsJSON, err := json.Marshal(params.Limits)
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: encode limits: %w", err)
	}
	issue, err := createOrchestrationIssue(ctx, qtx, tx, orchestrationIssueParams{
		WorkspaceID: params.WorkspaceID,
		Title:       params.Title,
		Description: params.Description,
		Status:      "backlog",
		CreatorType: "member",
		CreatorID:   params.ActorID,
		ProjectID:   params.ProjectID,
	})
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: %w", err)
	}
	mission, err := qtx.CreateMissionRecord(ctx, db.CreateMissionRecordParams{
		IssueID:     issue.ID,
		WorkspaceID: params.WorkspaceID,
		Limits:      limitsJSON,
		CreatedBy:   params.ActorID,
	})
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission record: %w", err)
	}
	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, mission.IssueID)
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: %w", err)
	}
	payload, err := json.Marshal(struct {
		MissionID string `json:"mission_id"`
	}{MissionID: uuidText(mission.IssueID)})
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: encode activity payload: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID:    params.WorkspaceID,
		MissionID:      mission.IssueID,
		Type:           activityMissionCreated,
		ActorType:      "user",
		ActorID:        params.ActorID,
		SubjectType:    activitySubjectMission,
		SubjectID:      mission.IssueID,
		CausationID:    params.CommandID,
		CorrelationID:  correlationID,
		PayloadVersion: 1,
		Payload:        payload,
		DedupeKey:      dedupeKey,
		Sequence:       sequence,
	})
	if err != nil {
		if isActivityDedupeViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return CreateMissionResult{}, fmt.Errorf("create mission: rollback command race: %w", rollbackErr)
			}
			return r.loadCreateMissionByDedupeKey(ctx, params.WorkspaceID, dedupeKey)
		}
		return CreateMissionResult{}, fmt.Errorf("create mission activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateMissionResult{}, fmt.Errorf("create mission: commit: %w", err)
	}
	return CreateMissionResult{Issue: issue, Mission: mission, Activity: activity}, nil
}

func (r *Repository) SubmitPlan(ctx context.Context, params SubmitPlanParams) (SubmitPlanResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: repository is not configured")
	}
	dedupeKey, err := commandDedupeKey(params.CommandID)
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: %w", err)
	}
	correlationID := correlationOrCommand(params.CorrelationID, params.CommandID)

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)

	activity, err := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: params.WorkspaceID,
		DedupeKey:   dedupeKey,
	})
	if err == nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: rollback idempotent transaction: %w", rollbackErr)
		}
		return r.loadSubmitPlanResult(ctx, params.WorkspaceID, params.MissionID, activity, true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: check command: %w", err)
	}
	planSource, err := normalizePlanSource(params.PlanSource, params.SourceArtifactID)
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: %w", err)
	}

	mission, err := qtx.LockMissionInWorkspace(ctx, db.LockMissionInWorkspaceParams{
		IssueID: params.MissionID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: lock mission: %w", err)
	}
	if mission.Status != string(MissionStatusDraft) || mission.Revision != params.ExpectedRevision {
		if replayed, replayErr := qtx.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
			WorkspaceID: params.WorkspaceID, DedupeKey: dedupeKey,
		}); replayErr == nil {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return SubmitPlanResult{}, fmt.Errorf("submit plan: rollback concurrent replay: %w", rollbackErr)
			}
			return r.loadSubmitPlanResult(ctx, params.WorkspaceID, params.MissionID, replayed, true)
		} else if !errors.Is(replayErr, pgx.ErrNoRows) {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: check concurrent replay: %w", replayErr)
		}
		if mission.Status != string(MissionStatusDraft) {
			return SubmitPlanResult{}, ErrMissionNotDraft
		}
		return SubmitPlanResult{}, ErrRevisionConflict
	}
	rootIssue, err := qtx.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: params.MissionID, WorkspaceID: params.WorkspaceID,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: load root issue: %w", err)
	}
	var planningAssignmentID pgtype.UUID
	if params.SourceArtifactID.Valid {
		planningAssignmentID, err = validatePlanProposalSource(ctx, qtx, params, mission)
		if err != nil {
			return SubmitPlanResult{}, err
		}
	}

	planJSON, err := json.Marshal(params.Plan)
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: encode plan: %w", err)
	}
	limitsJSON, err := json.Marshal(params.Plan.Limits)
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: encode limits: %w", err)
	}
	mission, err = qtx.AcceptMissionPlan(ctx, db.AcceptMissionPlanParams{
		PlanKey:           pgtype.Text{String: params.Plan.PlanKey, Valid: true},
		PlanSchemaVersion: pgtype.Int4{Int32: int32(params.Plan.SchemaVersion), Valid: true},
		Plan:              planJSON,
		Limits:            limitsJSON,
		IssueID:           params.MissionID,
		WorkspaceID:       params.WorkspaceID,
		ExpectedRevision:  params.ExpectedRevision,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: accept mission plan: %w", err)
	}
	if planningAssignmentID.Valid {
		if _, err := qtx.EndOrchestrationAssignment(ctx, db.EndOrchestrationAssignmentParams{
			TargetStatus: string(AssignmentStatusFulfilled), AssignmentID: planningAssignmentID,
			WorkspaceID: params.WorkspaceID, MissionID: params.MissionID,
		}); err != nil {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: fulfill planning assignment: %w", err)
		}
	}

	creatorType := issueCreatorType(params.ActorType)
	nodeIDs := make(map[string]pgtype.UUID, len(params.Plan.Nodes))
	taskNodes := make([]db.TaskNode, 0, len(params.Plan.Nodes))
	for _, node := range params.Plan.Nodes {
		issue, createErr := createOrchestrationIssue(ctx, qtx, tx, orchestrationIssueParams{
			WorkspaceID:   params.WorkspaceID,
			Title:         node.Title,
			Description:   pgtype.Text{String: node.Description, Valid: true},
			Status:        "backlog",
			CreatorType:   creatorType,
			CreatorID:     params.ActorID,
			ParentIssueID: params.MissionID,
			ProjectID:     rootIssue.ProjectID,
		})
		if createErr != nil {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: create issue for node %q: %w", node.Key, createErr)
		}
		criteria, marshalErr := json.Marshal(node.AcceptanceCriteria)
		if marshalErr != nil {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: encode acceptance criteria for node %q: %w", node.Key, marshalErr)
		}
		artifactKinds, marshalErr := json.Marshal(node.ArtifactKinds)
		if marshalErr != nil {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: encode artifact kinds for node %q: %w", node.Key, marshalErr)
		}
		taskNode, createErr := qtx.CreateTaskNodeRecord(ctx, db.CreateTaskNodeRecordParams{
			IssueID:                    issue.ID,
			WorkspaceID:                params.WorkspaceID,
			MissionID:                  params.MissionID,
			NodeKey:                    node.Key,
			Role:                       node.Duty.String(),
			AcceptanceCriteria:         criteria,
			ArtifactKinds:              artifactKinds,
			BudgetEstimateTokens:       node.BudgetEstimate.Tokens,
			BudgetEstimateCostUsdTicks: node.BudgetEstimate.CostUSDTicks,
		})
		if createErr != nil {
			return SubmitPlanResult{}, fmt.Errorf("submit plan: create task node %q: %w", node.Key, createErr)
		}
		nodeIDs[node.Key] = issue.ID
		taskNodes = append(taskNodes, taskNode)
	}

	dependencies := make([]db.IssueDependency, 0)
	for _, node := range params.Plan.Nodes {
		for _, dependencyKey := range node.DependsOn {
			dependencyID, exists := nodeIDs[dependencyKey]
			if !exists {
				return SubmitPlanResult{}, fmt.Errorf("submit plan: dependency %q for node %q was not persisted", dependencyKey, node.Key)
			}
			dependency, createErr := qtx.CreateOrchestrationIssueDependency(ctx, db.CreateOrchestrationIssueDependencyParams{
				IssueID: nodeIDs[node.Key], DependsOnIssueID: dependencyID,
			})
			if createErr != nil {
				return SubmitPlanResult{}, fmt.Errorf("submit plan: create dependency %q -> %q: %w", node.Key, dependencyKey, createErr)
			}
			dependencies = append(dependencies, dependency)
		}
	}
	if _, err := qtx.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: params.MissionID, Status: "todo", WorkspaceID: params.WorkspaceID,
	}); err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: update root issue compatibility status: %w", err)
	}
	sequence, err := allocateActivitySequence(ctx, qtx, params.WorkspaceID, params.MissionID)
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: %w", err)
	}
	sourceArtifactID := ""
	if params.SourceArtifactID.Valid {
		sourceArtifactID = uuidText(params.SourceArtifactID)
	}
	payload, err := json.Marshal(struct {
		PlanKey          string `json:"plan_key"`
		NodeCount        int    `json:"node_count"`
		SourceArtifactID string `json:"source_artifact_id,omitempty"`
		PlanSource       string `json:"plan_source"`
	}{PlanKey: params.Plan.PlanKey, NodeCount: len(params.Plan.Nodes), SourceArtifactID: sourceArtifactID, PlanSource: string(planSource)})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: encode activity payload: %w", err)
	}
	activity, err = qtx.CreateOrchestrationActivity(ctx, db.CreateOrchestrationActivityParams{
		WorkspaceID:    params.WorkspaceID,
		MissionID:      params.MissionID,
		Type:           activityMissionPlanAccepted,
		ActorType:      params.ActorType,
		ActorID:        params.ActorID,
		SubjectType:    activitySubjectMission,
		SubjectID:      params.MissionID,
		CausationID:    params.CommandID,
		CorrelationID:  correlationID,
		PayloadVersion: 1,
		Payload:        payload,
		DedupeKey:      dedupeKey,
		Sequence:       sequence,
	})
	if err != nil {
		if isActivityDedupeViolation(err) {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return SubmitPlanResult{}, fmt.Errorf("submit plan: rollback command race: %w", rollbackErr)
			}
			return r.loadSubmitPlanByDedupeKey(ctx, params.WorkspaceID, params.MissionID, dedupeKey)
		}
		return SubmitPlanResult{}, fmt.Errorf("submit plan: create activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitPlanResult{}, fmt.Errorf("submit plan: commit: %w", err)
	}
	return SubmitPlanResult{
		Mission: mission, TaskNodes: taskNodes, Dependencies: dependencies, Activity: activity,
	}, nil
}

func normalizePlanSource(source PlanSource, sourceArtifactID pgtype.UUID) (PlanSource, error) {
	if source == "" {
		if sourceArtifactID.Valid {
			return PlanSourceProposal, nil
		}
		return PlanSourceManual, nil
	}
	if sourceArtifactID.Valid {
		if source != PlanSourceProposal {
			return "", fmt.Errorf("source artifact requires proposal plan source")
		}
		return source, nil
	}
	if source == PlanSourceProposal {
		return "", fmt.Errorf("proposal plan source requires source artifact")
	}
	if source != PlanSourceManual && source != PlanSourceFixedTemplate {
		return "", fmt.Errorf("unsupported plan source %q", source)
	}
	return source, nil
}

func validatePlanProposalSource(ctx context.Context, qtx *db.Queries, params SubmitPlanParams, mission db.Mission) (pgtype.UUID, error) {
	artifact, err := qtx.GetArtifactInWorkspace(ctx, db.GetArtifactInWorkspaceParams{ArtifactID: params.SourceArtifactID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("submit plan: load source artifact: %w", err)
	}
	if artifact.MissionID != mission.IssueID || artifact.TaskNodeID.Valid || artifact.Kind != string(ArtifactKindPlanProposal) {
		return pgtype.UUID{}, fmt.Errorf("submit plan: source artifact is not a Mission-scoped PlanProposal")
	}
	run, err := qtx.GetOrchestrationRunInWorkspace(ctx, db.GetOrchestrationRunInWorkspaceParams{RunID: artifact.RunID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("submit plan: load source run: %w", err)
	}
	if run.MissionID != mission.IssueID || run.TaskNodeID.Valid || run.Purpose != "plan" || run.Status != string(RunStatusSucceeded) {
		return pgtype.UUID{}, fmt.Errorf("submit plan: source artifact does not belong to a successful Planning Run")
	}
	assignment, err := qtx.GetOrchestrationAssignmentInWorkspace(ctx, db.GetOrchestrationAssignmentInWorkspaceParams{AssignmentID: run.AssignmentID, WorkspaceID: params.WorkspaceID})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("submit plan: load source assignment: %w", err)
	}
	if assignment.MissionID != mission.IssueID || assignment.TaskNodeID.Valid || assignment.Role != string(DutyPlanner) || assignment.Status != string(AssignmentStatusActive) {
		return pgtype.UUID{}, fmt.Errorf("submit plan: source artifact does not belong to the active Planning Assignment")
	}
	proposal, validationErrs := DecodeAndValidatePlanProposal(artifact.Metadata, uuidText(mission.IssueID), params.Plan.Limits)
	if len(validationErrs) > 0 {
		return pgtype.UUID{}, CommandValidationError{Errors: validationErrs}
	}
	if !reflect.DeepEqual(PlanFromProposal(proposal), params.Plan) {
		return pgtype.UUID{}, fmt.Errorf("submit plan: source artifact payload does not match the submitted plan")
	}
	canonical, err := EncodePlanProposal(proposal)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("submit plan: encode source artifact: %w", err)
	}
	if !artifact.ContentHash.Valid || artifact.ContentHash.String != planProposalContentHash(canonical) {
		return pgtype.UUID{}, fmt.Errorf("submit plan: source artifact content hash mismatch")
	}
	return assignment.ID, nil
}

type orchestrationIssueParams struct {
	WorkspaceID   pgtype.UUID
	Title         string
	Description   pgtype.Text
	Status        string
	CreatorType   string
	CreatorID     pgtype.UUID
	ParentIssueID pgtype.UUID
	ProjectID     pgtype.UUID
}

func createOrchestrationIssue(ctx context.Context, queries *db.Queries, tx pgx.Tx, params orchestrationIssueParams) (db.Issue, error) {
	number, err := queries.IncrementIssueCounter(ctx, params.WorkspaceID)
	if err != nil {
		return db.Issue{}, fmt.Errorf("increment issue counter: %w", err)
	}
	position, err := issueposition.NextTopPosition(ctx, tx, params.WorkspaceID, params.Status)
	if err != nil {
		return db.Issue{}, fmt.Errorf("next issue position: %w", err)
	}
	issue, err := queries.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:   params.WorkspaceID,
		Title:         params.Title,
		Description:   params.Description,
		Status:        params.Status,
		Priority:      "none",
		CreatorType:   params.CreatorType,
		CreatorID:     params.CreatorID,
		ParentIssueID: params.ParentIssueID,
		Position:      position,
		Number:        number,
		ProjectID:     params.ProjectID,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("insert issue: %w", err)
	}
	return issue, nil
}

func allocateActivitySequence(ctx context.Context, queries *db.Queries, workspaceID, missionID pgtype.UUID) (int64, error) {
	sequence, err := queries.AllocateMissionActivitySequence(ctx, db.AllocateMissionActivitySequenceParams{
		IssueID: missionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return 0, fmt.Errorf("allocate activity sequence: %w", err)
	}
	return sequence, nil
}

func (r *Repository) loadCreateMissionByDedupeKey(ctx context.Context, workspaceID pgtype.UUID, dedupeKey string) (CreateMissionResult, error) {
	activity, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: workspaceID, DedupeKey: dedupeKey,
	})
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("load create mission command result: %w", err)
	}
	return r.loadCreateMissionResult(ctx, workspaceID, activity, true)
}

func (r *Repository) loadCreateMissionResult(ctx context.Context, workspaceID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (CreateMissionResult, error) {
	if activity.Type != activityMissionCreated || activity.SubjectType != activitySubjectMission {
		return CreateMissionResult{}, ErrCommandConflict
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{
		IssueID: activity.MissionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("load mission command result: %w", err)
	}
	issue, err := r.queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: activity.MissionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return CreateMissionResult{}, fmt.Errorf("load mission issue command result: %w", err)
	}
	return CreateMissionResult{Issue: issue, Mission: mission, Activity: activity, Idempotent: idempotent}, nil
}

func (r *Repository) loadSubmitPlanByDedupeKey(ctx context.Context, workspaceID, missionID pgtype.UUID, dedupeKey string) (SubmitPlanResult, error) {
	activity, err := r.queries.GetOrchestrationActivityByDedupeKey(ctx, db.GetOrchestrationActivityByDedupeKeyParams{
		WorkspaceID: workspaceID, DedupeKey: dedupeKey,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("load submit plan command result: %w", err)
	}
	return r.loadSubmitPlanResult(ctx, workspaceID, missionID, activity, true)
}

func (r *Repository) loadSubmitPlanResult(ctx context.Context, workspaceID, missionID pgtype.UUID, activity db.OrchestrationActivity, idempotent bool) (SubmitPlanResult, error) {
	if activity.Type != activityMissionPlanAccepted || activity.SubjectType != activitySubjectMission || activity.MissionID != missionID {
		return SubmitPlanResult{}, ErrCommandConflict
	}
	mission, err := r.queries.GetMissionInWorkspace(ctx, db.GetMissionInWorkspaceParams{
		IssueID: missionID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("load planned mission command result: %w", err)
	}
	taskNodes, err := r.queries.ListTaskNodesByMission(ctx, db.ListTaskNodesByMissionParams{
		WorkspaceID: workspaceID, MissionID: missionID,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("load task nodes command result: %w", err)
	}
	dependencies, err := r.queries.ListOrchestrationIssueDependencies(ctx, db.ListOrchestrationIssueDependenciesParams{
		WorkspaceID: workspaceID, MissionID: missionID,
	})
	if err != nil {
		return SubmitPlanResult{}, fmt.Errorf("load dependencies command result: %w", err)
	}
	return SubmitPlanResult{
		Mission: mission, TaskNodes: taskNodes, Dependencies: dependencies,
		Activity: activity, Idempotent: idempotent,
	}, nil
}

func commandDedupeKey(commandID pgtype.UUID) (string, error) {
	if !commandID.Valid {
		return "", fmt.Errorf("command id is required")
	}
	return "command:" + uuidText(commandID), nil
}

func correlationOrCommand(correlationID, commandID pgtype.UUID) pgtype.UUID {
	if correlationID.Valid {
		return correlationID
	}
	return commandID
}

func issueCreatorType(actorType string) string {
	if actorType == "agent" {
		return "agent"
	}
	return "member"
}

func uuidText(value pgtype.UUID) string {
	return uuid.UUID(value.Bytes).String()
}

func isActivityDedupeViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "idx_orchestration_activity_dedupe_unique"
}

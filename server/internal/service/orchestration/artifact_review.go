package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

type ReviewDecision string

const (
	ReviewDecisionApproved         ReviewDecision = "approved"
	ReviewDecisionChangesRequested ReviewDecision = "changes_requested"
	ReviewDecisionRejected         ReviewDecision = "rejected"
)

type RecordArtifactCommand struct {
	WorkspaceID   pgtype.UUID
	MissionID     pgtype.UUID
	TaskNodeID    pgtype.UUID
	RunID         pgtype.UUID
	CommandID     pgtype.UUID
	CorrelationID pgtype.UUID
	ActorID       pgtype.UUID
	Kind          ArtifactKind
	URI           string
	ContentHash   string
	Summary       string
	Metadata      json.RawMessage
}

type RecordArtifactResult struct {
	Artifact   db.Artifact
	Activity   db.OrchestrationActivity
	Advance    AdvanceMissionResult
	Idempotent bool
}

type RecordReviewVerdictCommand struct {
	WorkspaceID      pgtype.UUID
	MissionID        pgtype.UUID
	TaskNodeID       pgtype.UUID
	ReviewRunID      pgtype.UUID
	ArtifactID       pgtype.UUID
	CommandID        pgtype.UUID
	CorrelationID    pgtype.UUID
	ActorID          pgtype.UUID
	Decision         ReviewDecision
	Evidence         json.RawMessage
	RequestedChanges []string
}

type RecordReviewVerdictResult struct {
	Verdict    db.ReviewVerdict
	TaskNode   db.TaskNode
	Activities []db.OrchestrationActivity
	Advance    AdvanceMissionResult
	Idempotent bool
}

func (s *Service) RecordArtifact(ctx context.Context, command RecordArtifactCommand) (RecordArtifactResult, error) {
	if s == nil || s.repository == nil {
		return RecordArtifactResult{}, fmt.Errorf("record artifact: service is not configured")
	}
	result, err := s.repository.RecordArtifact(ctx, command)
	if err != nil {
		return RecordArtifactResult{}, err
	}
	advance, err := s.AdvanceMission(ctx, AdvanceMissionCommand{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, CorrelationID: correlationOrCommand(command.CorrelationID, command.CommandID)})
	result.Advance = advance
	if err != nil {
		return result, fmt.Errorf("record artifact: advance mission: %w", err)
	}
	return result, nil
}

func (s *Service) RecordReviewVerdict(ctx context.Context, command RecordReviewVerdictCommand) (RecordReviewVerdictResult, error) {
	if s == nil || s.repository == nil {
		return RecordReviewVerdictResult{}, fmt.Errorf("record review verdict: service is not configured")
	}
	result, err := s.repository.RecordReviewVerdict(ctx, command)
	if err != nil {
		return RecordReviewVerdictResult{}, err
	}
	advance, err := s.AdvanceMission(ctx, AdvanceMissionCommand{WorkspaceID: command.WorkspaceID, MissionID: command.MissionID, CorrelationID: correlationOrCommand(command.CorrelationID, command.CommandID)})
	result.Advance = advance
	if err != nil {
		return result, fmt.Errorf("record review verdict: advance mission: %w", err)
	}
	return result, nil
}

func validateArtifactCommand(command RecordArtifactCommand) error {
	if !validUUID(command.WorkspaceID) || !validUUID(command.MissionID) || !validUUID(command.TaskNodeID) || !validUUID(command.RunID) || !validUUID(command.CommandID) || !validUUID(command.ActorID) {
		return fmt.Errorf("record artifact: identity fields are required")
	}
	if _, ok := allowedArtifactKinds[command.Kind]; !ok {
		return fmt.Errorf("record artifact: unsupported kind %q", command.Kind)
	}
	if strings.TrimSpace(command.URI) == "" || len(command.URI) > 4096 {
		return fmt.Errorf("record artifact: uri must contain 1 to 4096 bytes")
	}
	return validateJSONObject("metadata", command.Metadata)
}

func validateReviewCommand(command RecordReviewVerdictCommand) error {
	if !validUUID(command.WorkspaceID) || !validUUID(command.MissionID) || !validUUID(command.TaskNodeID) || !validUUID(command.ReviewRunID) || !validUUID(command.ArtifactID) || !validUUID(command.CommandID) || !validUUID(command.ActorID) {
		return fmt.Errorf("record review verdict: identity fields are required")
	}
	switch command.Decision {
	case ReviewDecisionApproved, ReviewDecisionChangesRequested, ReviewDecisionRejected:
	default:
		return fmt.Errorf("record review verdict: unsupported decision %q", command.Decision)
	}
	if command.Decision == ReviewDecisionChangesRequested && len(command.RequestedChanges) == 0 {
		return fmt.Errorf("record review verdict: requested_changes are required")
	}
	for _, change := range command.RequestedChanges {
		if strings.TrimSpace(change) == "" {
			return fmt.Errorf("record review verdict: requested_changes cannot contain empty values")
		}
	}
	return validateJSONObject("evidence", command.Evidence)
}

func validateJSONObject(name string, value json.RawMessage) error {
	if len(value) == 0 {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return nil
}

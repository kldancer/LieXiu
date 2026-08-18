package orchestration

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	quickCreatePlanKey       = "quick-create-v1"
	quickCreateTitleMaxRunes = 120
)

// QuickCreateMissionCommand is the compatibility command behind the former
// Issue quick-create endpoint. It creates a planned Mission, but deliberately
// does not start or dispatch it: the owner remains the explicit authority for
// StartMission.
type QuickCreateMissionCommand struct {
	WorkspaceID pgtype.UUID
	CommandID   pgtype.UUID
	ActorID     pgtype.UUID
	Prompt      string
	ProjectID   pgtype.UUID
}

type QuickCreateMissionResult struct {
	MissionID pgtype.UUID
	Status    MissionStatus
	Revision  int64
	Replayed  bool
}

// QuickCreateMission composes the existing idempotent CreateMission and
// SubmitPlan commands. The submit command is derived from the caller's command
// ID, so retrying after a crash between the two transactions safely completes
// the draft instead of creating another Mission or plan.
func (s *Service) QuickCreateMission(ctx context.Context, command QuickCreateMissionCommand) (QuickCreateMissionResult, error) {
	prompt := strings.TrimSpace(command.Prompt)
	create := CreateMissionCommand{
		WorkspaceID: command.WorkspaceID,
		CommandID:   command.CommandID,
		ActorID:     command.ActorID,
		Title:       quickCreateMissionTitle(prompt),
		Description: pgtype.Text{String: prompt, Valid: prompt != ""},
		ProjectID:   command.ProjectID,
		Limits:      quickCreateMissionLimits(),
	}
	created, err := s.CreateMission(ctx, create)
	if err != nil {
		return QuickCreateMissionResult{}, err
	}

	// Always derive the plan from the persisted Mission description. If a
	// caller accidentally reuses a command_id with a different prompt after the
	// create transaction committed, recovery preserves the original command's
	// meaning instead of submitting a plan for the new payload.
	persistedPrompt := strings.TrimSpace(created.Issue.Description.String)
	planCommandID := derivedQuickCreateCommandID(command.CommandID, "submit-plan")
	planned, err := s.SubmitPlan(ctx, SubmitPlanCommand{
		WorkspaceID:      command.WorkspaceID,
		MissionID:        created.Mission.IssueID,
		CommandID:        planCommandID,
		CorrelationID:    command.CommandID,
		ActorID:          command.ActorID,
		ExpectedRevision: created.Mission.Revision,
		Plan:             quickCreateMissionPlan(created.Mission.IssueID, persistedPrompt),
		Source:           PlanSourceFixedTemplate,
	})
	if err != nil {
		return QuickCreateMissionResult{}, err
	}

	return QuickCreateMissionResult{
		MissionID: planned.Mission.IssueID,
		Status:    MissionStatus(planned.Mission.Status),
		Revision:  planned.Mission.Revision,
		Replayed:  created.Idempotent || planned.Idempotent,
	}, nil
}

func quickCreateMissionPlan(missionID pgtype.UUID, prompt string) Plan {
	limits := quickCreateMissionLimits()
	return Plan{
		SchemaVersion: PlanSchemaVersion,
		MissionID:     uuidText(missionID),
		PlanKey:       quickCreatePlanKey,
		Limits:        limits,
		Nodes: []PlanNode{
			{
				Key:         "execute",
				Title:       "Execute requested outcome",
				Description: prompt,
				Duty:        DutyExecutor,
				AcceptanceCriteria: []string{
					"Complete the requested outcome and provide verifiable evidence.",
				},
				ArtifactKinds:  []ArtifactKind{ArtifactKindFile, ArtifactKindTestReceipt},
				BudgetEstimate: BudgetEstimate{Tokens: 250_000},
			},
			{
				Key:         "integrate",
				Title:       "Integrate and deliver",
				Description: "Review the execution evidence, resolve delivery gaps, and prepare the final result.",
				Duty:        DutyIntegrator,
				AcceptanceCriteria: []string{
					"The final delivery traces the requested outcome to its evidence and remaining risks.",
				},
				ArtifactKinds:  []ArtifactKind{ArtifactKindFinalDelivery},
				DependsOn:      []string{"execute"},
				BudgetEstimate: BudgetEstimate{Tokens: 250_000},
			},
		},
	}
}

func quickCreateMissionLimits() PlanLimits {
	maxTokens := int64(1_000_000)
	return PlanLimits{
		MaxParallelRuns: 1,
		MaxTaskAttempts: 2,
		MaxReworkCycles: 1,
		Budget: &BudgetPolicy{
			MaxTokens: &maxTokens,
			Gate:      BudgetGateOwnerApproval,
		},
	}
}

func quickCreateMissionTitle(prompt string) string {
	title := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if utf8.RuneCountInString(title) <= quickCreateTitleMaxRunes {
		return title
	}
	runes := []rune(title)
	return strings.TrimSpace(string(runes[:quickCreateTitleMaxRunes]))
}

func derivedQuickCreateCommandID(commandID pgtype.UUID, purpose string) pgtype.UUID {
	derived := uuid.NewSHA1(uuid.UUID(commandID.Bytes), []byte("quick-create:"+purpose))
	return pgtype.UUID{Bytes: [16]byte(derived), Valid: true}
}

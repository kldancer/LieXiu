package orchestration

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	PlanningRunSpecSchemaVersion = 1
	PlanProposalSchemaVersion    = 1
	maxPlanProposalContextRefs   = 32
	maxPlanProposalCriteria      = 32
	maxPlanProposalTextBytes     = 32 * 1024
	maxPlanProposalReferenceSize = 4 * 1024
)

// PlanProposal is the versioned, runtime-neutral payload of a plan_proposal
// Artifact. Artifact row identity, immutable version, lineage, and content hash
// remain repository facts; this payload contains only deterministic plan input
// and output.
type PlanProposal struct {
	SchemaVersion int                `json:"schema_version"`
	MissionID     string             `json:"mission_id"`
	ProposalKey   string             `json:"proposal_key"`
	Input         PlanProposalInput  `json:"input"`
	Limits        PlanLimits         `json:"limits"`
	Nodes         []PlanProposalNode `json:"nodes"`
}

type PlanProposalInput struct {
	Objective        string                   `json:"objective"`
	ContextRefs      []PlanProposalContextRef `json:"context_refs"`
	DeliveryCriteria []string                 `json:"delivery_criteria"`
}

type PlanProposalContextRef struct {
	Kind        string `json:"kind"`
	URI         string `json:"uri"`
	ContentHash string `json:"content_hash,omitempty"`
}

type PlanProposalNode struct {
	Key                string         `json:"key"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Duty               Duty           `json:"duty"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	ArtifactKinds      []ArtifactKind `json:"artifact_kinds"`
	DependsOn          []string       `json:"depends_on"`
	BudgetEstimate     BudgetEstimate `json:"budget_estimate"`
}

type PlanningRunSpec struct {
	SchemaVersion         int               `json:"schema_version"`
	MissionID             string            `json:"mission_id"`
	ProposalArtifactKind  ArtifactKind      `json:"proposal_artifact_kind"`
	ProposalSchemaVersion int               `json:"proposal_schema_version"`
	Input                 PlanProposalInput `json:"input"`
	Limits                PlanLimits        `json:"limits"`
}

type validatedPlanningProposal struct {
	Proposal  PlanProposal
	Canonical []byte
}

func EncodePlanningRunSpec(spec PlanningRunSpec) ([]byte, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("encode planning run spec: %w", err)
	}
	return data, nil
}

func validatePlanningTaskProposal(mission db.Mission, run db.OrchestrationRun, task db.AgentTaskQueue) (validatedPlanningProposal, []ValidationError) {
	invalid := func(path, code, message string) (validatedPlanningProposal, []ValidationError) {
		return validatedPlanningProposal{}, []ValidationError{{Path: path, Code: code, Message: message}}
	}
	var completion struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(task.Result, &completion); err != nil {
		return invalid("$", "invalid_task_result", fmt.Sprintf("decode task result: %v", err))
	}
	if strings.TrimSpace(completion.Output) == "" {
		return invalid("$", "missing_plan_proposal", "planner output must contain a PlanProposal JSON value")
	}
	var spec PlanningRunSpec
	if err := json.Unmarshal(run.Input, &spec); err != nil {
		return invalid("$", "invalid_planning_run_spec", fmt.Sprintf("decode planning run spec: %v", err))
	}
	if spec.SchemaVersion != PlanningRunSpecSchemaVersion || spec.MissionID != uuidText(mission.IssueID) || spec.ProposalArtifactKind != ArtifactKindPlanProposal || spec.ProposalSchemaVersion != PlanProposalSchemaVersion {
		return invalid("$", "invalid_planning_run_spec", "planning run spec does not match the active PlanProposal contract")
	}
	proposal, errs := DecodeAndValidatePlanProposal([]byte(completion.Output), uuidText(mission.IssueID), spec.Limits)
	if len(errs) > 0 {
		return validatedPlanningProposal{}, errs
	}
	if !reflect.DeepEqual(proposal.Input, spec.Input) {
		errs = append(errs, ValidationError{Path: "input", Code: "planning_input_mismatch", Message: "proposal input must match the frozen planning run input"})
	}
	if !reflect.DeepEqual(proposal.Limits, spec.Limits) {
		errs = append(errs, ValidationError{Path: "limits", Code: "planning_limits_mismatch", Message: "proposal limits must match the frozen planning run limits"})
	}
	if len(errs) > 0 {
		return validatedPlanningProposal{}, errs
	}
	canonical, err := EncodePlanProposal(proposal)
	if err != nil {
		return invalid("$", "invalid_plan_proposal", err.Error())
	}
	return validatedPlanningProposal{Proposal: proposal, Canonical: canonical}, nil
}

func planningProposalFailure(errors []ValidationError) pgtype.Text {
	payload, err := json.Marshal(errors)
	if err != nil {
		return textValue("invalid plan proposal")
	}
	return textValue(normalizeFailureMessage(string(payload), "invalid plan proposal"))
}

func EncodePlanProposal(proposal PlanProposal) ([]byte, error) {
	data, err := json.Marshal(proposal)
	if err != nil {
		return nil, fmt.Errorf("encode plan proposal: %w", err)
	}
	return data, nil
}

// PlanFromProposal preserves Duty without introducing a second role enum when
// a proposal is materialized through the existing SubmitPlan path.
func PlanFromProposal(proposal PlanProposal) Plan {
	nodes := make([]PlanNode, 0, len(proposal.Nodes))
	for _, node := range proposal.Nodes {
		nodes = append(nodes, PlanNode{
			Key: node.Key, Title: node.Title, Description: node.Description, Duty: node.Duty,
			AcceptanceCriteria: node.AcceptanceCriteria, ArtifactKinds: node.ArtifactKinds,
			DependsOn: node.DependsOn, BudgetEstimate: node.BudgetEstimate,
		})
	}
	return Plan{
		SchemaVersion: PlanSchemaVersion, MissionID: proposal.MissionID,
		PlanKey: proposal.ProposalKey, Limits: proposal.Limits, Nodes: nodes,
	}
}

func planProposalContentHash(canonical []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
}

func DecodePlanProposal(data []byte) (PlanProposal, error) {
	var proposal PlanProposal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		return PlanProposal{}, fmt.Errorf("decode plan proposal: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return PlanProposal{}, fmt.Errorf("decode plan proposal: multiple JSON values")
		}
		return PlanProposal{}, fmt.Errorf("decode plan proposal: %w", err)
	}
	return proposal, nil
}

// DecodeAndValidatePlanProposal keeps malformed runtime output on the same
// structured rejection surface as a syntactically valid but illegal proposal.
func DecodeAndValidatePlanProposal(data []byte, expectedMissionID string, hardLimits PlanLimits) (PlanProposal, []ValidationError) {
	proposal, err := DecodePlanProposal(data)
	if err != nil {
		return PlanProposal{}, []ValidationError{{
			Path: "$", Code: "invalid_proposal_schema", Message: err.Error(),
		}}
	}
	return proposal, ValidatePlanProposal(proposal, expectedMissionID, hardLimits)
}

func ValidatePlanProposal(proposal PlanProposal, expectedMissionID string, hardLimits PlanLimits) []ValidationError {
	var errs []ValidationError
	add := func(path, code, message string) {
		errs = append(errs, ValidationError{Path: path, Code: code, Message: message})
	}

	if proposal.SchemaVersion != PlanProposalSchemaVersion {
		add("schema_version", "unsupported_schema_version", fmt.Sprintf("schema_version must be %d", PlanProposalSchemaVersion))
	}
	errs = append(errs, ValidatePlanProposalInput(proposal.Input)...)

	plan := Plan{
		SchemaVersion: PlanSchemaVersion,
		MissionID:     proposal.MissionID,
		PlanKey:       proposal.ProposalKey,
		Limits:        proposal.Limits,
		Nodes:         make([]PlanNode, 0, len(proposal.Nodes)),
	}
	for index, node := range proposal.Nodes {
		if node.Duty != DutyExecutor && node.Duty != DutyIntegrator {
			add(fmt.Sprintf("nodes[%d].duty", index), "invalid_node_duty", "task node duty must be executor or integrator")
		}
		plan.Nodes = append(plan.Nodes, PlanNode{
			Key: node.Key, Title: node.Title, Description: node.Description, Duty: node.Duty,
			AcceptanceCriteria: node.AcceptanceCriteria, ArtifactKinds: node.ArtifactKinds,
			DependsOn: node.DependsOn, BudgetEstimate: node.BudgetEstimate,
		})
	}
	for _, planErr := range ValidatePlan(plan, expectedMissionID, hardLimits) {
		if planErr.Code == "invalid_node_duty" {
			continue
		}
		planErr.Path = strings.Replace(planErr.Path, "plan_key", "proposal_key", 1)
		errs = append(errs, planErr)
	}
	return errs
}

// ValidatePlanProposalInput applies the same bounded input contract before a
// planning Run is persisted and after a runtime returns its PlanProposal.
func ValidatePlanProposalInput(input PlanProposalInput) []ValidationError {
	var errs []ValidationError
	add := func(path, code, message string) {
		errs = append(errs, ValidationError{Path: path, Code: code, Message: message})
	}
	if strings.TrimSpace(input.Objective) == "" {
		add("input.objective", "missing_objective", "objective is required")
	} else if len(input.Objective) > maxPlanProposalTextBytes {
		add("input.objective", "objective_too_long", fmt.Sprintf("objective may not exceed %d bytes", maxPlanProposalTextBytes))
	}
	if len(input.ContextRefs) > maxPlanProposalContextRefs {
		add("input.context_refs", "context_ref_limit_exceeded", fmt.Sprintf("context_refs may contain at most %d items", maxPlanProposalContextRefs))
	}
	for index, ref := range input.ContextRefs {
		path := fmt.Sprintf("input.context_refs[%d]", index)
		if strings.TrimSpace(ref.Kind) == "" {
			add(path+".kind", "missing_context_kind", "context kind is required")
		}
		if strings.TrimSpace(ref.URI) == "" {
			add(path+".uri", "missing_context_uri", "context URI is required")
		} else if len(ref.URI) > maxPlanProposalReferenceSize {
			add(path+".uri", "context_uri_too_long", fmt.Sprintf("context URI may not exceed %d bytes", maxPlanProposalReferenceSize))
		}
		if len(ref.ContentHash) > maxPlanProposalReferenceSize {
			add(path+".content_hash", "context_hash_too_long", fmt.Sprintf("content hash may not exceed %d bytes", maxPlanProposalReferenceSize))
		}
	}
	if len(input.DeliveryCriteria) == 0 {
		add("input.delivery_criteria", "missing_delivery_criteria", "at least one delivery criterion is required")
	} else if len(input.DeliveryCriteria) > maxPlanProposalCriteria {
		add("input.delivery_criteria", "delivery_criteria_limit_exceeded", fmt.Sprintf("delivery_criteria may contain at most %d items", maxPlanProposalCriteria))
	}
	for index, criterion := range input.DeliveryCriteria {
		path := fmt.Sprintf("input.delivery_criteria[%d]", index)
		if strings.TrimSpace(criterion) == "" {
			add(path, "empty_delivery_criterion", "delivery criteria cannot be empty")
		} else if len(criterion) > maxPlanProposalReferenceSize {
			add(path, "delivery_criterion_too_long", fmt.Sprintf("delivery criteria may not exceed %d bytes", maxPlanProposalReferenceSize))
		}
	}
	return errs
}

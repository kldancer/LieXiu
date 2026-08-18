package orchestration

type MissionStatus string

// PlanSource is the owner-visible provenance of the accepted plan. It is
// metadata on the existing SubmitPlan transaction, not a separate lifecycle.
type PlanSource string

const (
	PlanSourceManual        PlanSource = "manual"
	PlanSourceProposal      PlanSource = "proposal"
	PlanSourceFixedTemplate PlanSource = "fixed_template"
)

const (
	MissionStatusDraft     MissionStatus = "draft"
	MissionStatusReady     MissionStatus = "ready"
	MissionStatusRunning   MissionStatus = "running"
	MissionStatusBlocked   MissionStatus = "blocked"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusFailed    MissionStatus = "failed"
	MissionStatusCancelled MissionStatus = "cancelled"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusReady     TaskStatus = "ready"
	TaskStatusAssigned  TaskStatus = "assigned"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusReview    TaskStatus = "review"
	TaskStatusRework    TaskStatus = "rework"
	TaskStatusBlocked   TaskStatus = "blocked"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type AssignmentStatus string

const (
	AssignmentStatusActive     AssignmentStatus = "active"
	AssignmentStatusFulfilled  AssignmentStatus = "fulfilled"
	AssignmentStatusSuperseded AssignmentStatus = "superseded"
	AssignmentStatusRevoked    AssignmentStatus = "revoked"
)

type RunStatus string

const (
	RunStatusQueued     RunStatus = "queued"
	RunStatusDispatched RunStatus = "dispatched"
	RunStatusRunning    RunStatus = "running"
	RunStatusSucceeded  RunStatus = "succeeded"
	RunStatusFailed     RunStatus = "failed"
	RunStatusCancelled  RunStatus = "cancelled"
)

// Duty is the complete set of business responsibilities understood by the
// orchestration state machine. Custom RoleProfile configuration must select
// one of these values rather than introducing another lifecycle branch.
type Duty string

const (
	DutyPlanner    Duty = "planner"
	DutyExecutor   Duty = "executor"
	DutyReviewer   Duty = "reviewer"
	DutyIntegrator Duty = "integrator"
)

func (d Duty) String() string { return string(d) }

func (d Duty) Valid() bool {
	switch d {
	case DutyPlanner, DutyExecutor, DutyReviewer, DutyIntegrator:
		return true
	default:
		return false
	}
}

func (d Duty) TaskNodeDuty() bool {
	return d == DutyExecutor || d == DutyIntegrator
}

type ArtifactKind string

const (
	ArtifactKindBranch        ArtifactKind = "branch"
	ArtifactKindCommit        ArtifactKind = "commit"
	ArtifactKindDiff          ArtifactKind = "diff"
	ArtifactKindFile          ArtifactKind = "file"
	ArtifactKindTestReceipt   ArtifactKind = "test_receipt"
	ArtifactKindFinalDelivery ArtifactKind = "final_delivery"
	ArtifactKindPlanProposal  ArtifactKind = "plan_proposal"
)

const (
	PlanSchemaVersion      = 1
	MaxPlanNodes           = 16
	MaxPlanDependencyDepth = 4
)

type PlanLimits struct {
	MaxParallelRuns int           `json:"max_parallel_runs"`
	MaxTaskAttempts int           `json:"max_task_attempts"`
	MaxReworkCycles int           `json:"max_rework_cycles"`
	Budget          *BudgetPolicy `json:"budget,omitempty"`
}

type BudgetPolicy struct {
	MaxTokens       *int64 `json:"max_tokens,omitempty"`
	MaxCostUSDTicks *int64 `json:"max_cost_usd_ticks,omitempty"`
	Gate            string `json:"gate"`
}

type BudgetEstimate struct {
	Tokens       int64 `json:"tokens"`
	CostUSDTicks int64 `json:"cost_usd_ticks"`
}

func DefaultPlanHardLimits() PlanLimits {
	return PlanLimits{
		MaxParallelRuns: 8,
		MaxTaskAttempts: 5,
		MaxReworkCycles: 3,
	}
}

type Plan struct {
	SchemaVersion int        `json:"schema_version"`
	MissionID     string     `json:"mission_id"`
	PlanKey       string     `json:"plan_key"`
	Limits        PlanLimits `json:"limits"`
	Nodes         []PlanNode `json:"nodes"`
}

type PlanNode struct {
	Key                string         `json:"key"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Duty               Duty           `json:"duty"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	ArtifactKinds      []ArtifactKind `json:"artifact_kinds"`
	DependsOn          []string       `json:"depends_on"`
	BudgetEstimate     BudgetEstimate `json:"budget_estimate"`
}

type ValidationError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type NodeSnapshot struct {
	Key                 string
	Status              TaskStatus
	Priority            int
	CreatedOrder        int
	DependencyKeys      []string
	HasActiveAssignment bool
}

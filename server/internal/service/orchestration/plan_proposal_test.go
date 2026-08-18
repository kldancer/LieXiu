package orchestration

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPlanProposalRoundTripPreservesVersionedContract(t *testing.T) {
	t.Parallel()

	proposal := validPlanProposal()
	encoded, err := EncodePlanProposal(proposal)
	if err != nil {
		t.Fatalf("EncodePlanProposal: %v", err)
	}
	encodedAgain, err := EncodePlanProposal(proposal)
	if err != nil {
		t.Fatalf("EncodePlanProposal again: %v", err)
	}
	if !bytes.Equal(encoded, encodedAgain) {
		t.Fatalf("EncodePlanProposal is not deterministic:\n%s\n%s", encoded, encodedAgain)
	}

	decoded, errs := DecodeAndValidatePlanProposal(encoded, "mission-1", DefaultPlanHardLimits())
	if len(errs) != 0 {
		t.Fatalf("DecodeAndValidatePlanProposal errors = %#v", errs)
	}
	if decoded.ProposalKey != proposal.ProposalKey || decoded.SchemaVersion != PlanProposalSchemaVersion {
		t.Fatalf("decoded proposal identity = %#v", decoded)
	}
	if len(decoded.Input.ContextRefs) != 1 || decoded.Input.ContextRefs[0].ContentHash != "sha256:input" {
		t.Fatalf("decoded input = %#v", decoded.Input)
	}
	if len(decoded.Nodes) != 3 || decoded.Nodes[2].Duty != DutyIntegrator {
		t.Fatalf("decoded nodes = %#v", decoded.Nodes)
	}
}

func TestDecodeAndValidatePlanProposalReturnsStructuredDecodeReason(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"schema_version": 1,
		"mission_id": "mission-1",
		"proposal_key": "proposal-1",
		"input": {"objective": "Deliver", "context_refs": [], "delivery_criteria": ["Accepted"]},
		"limits": {"max_parallel_runs": 2, "max_task_attempts": 2, "max_rework_cycles": 1},
		"nodes": [],
		"unexpected": true
	}`)
	_, errs := DecodeAndValidatePlanProposal(raw, "mission-1", DefaultPlanHardLimits())
	if len(errs) != 1 || errs[0].Path != "$" || errs[0].Code != "invalid_proposal_schema" {
		t.Fatalf("decode errors = %#v", errs)
	}
}

func TestValidatePlanProposalReturnsStructuredContractReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*PlanProposal)
		code string
	}{
		{
			name: "unsupported schema",
			edit: func(proposal *PlanProposal) { proposal.SchemaVersion++ },
			code: "unsupported_schema_version",
		},
		{
			name: "mission mismatch",
			edit: func(proposal *PlanProposal) { proposal.MissionID = "another-mission" },
			code: "mission_mismatch",
		},
		{
			name: "missing objective",
			edit: func(proposal *PlanProposal) { proposal.Input.Objective = "" },
			code: "missing_objective",
		},
		{
			name: "invalid context ref",
			edit: func(proposal *PlanProposal) { proposal.Input.ContextRefs[0].URI = "" },
			code: "missing_context_uri",
		},
		{
			name: "empty delivery criterion",
			edit: func(proposal *PlanProposal) { proposal.Input.DeliveryCriteria[0] = "" },
			code: "empty_delivery_criterion",
		},
		{
			name: "reviewer task node",
			edit: func(proposal *PlanProposal) { proposal.Nodes[0].Duty = DutyReviewer },
			code: "invalid_node_duty",
		},
		{
			name: "invalid graph",
			edit: func(proposal *PlanProposal) {
				proposal.Nodes[2].DependsOn = append(proposal.Nodes[2].DependsOn, "missing")
			},
			code: "unknown_dependency",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			proposal := validPlanProposal()
			tt.edit(&proposal)
			errs := ValidatePlanProposal(proposal, "mission-1", DefaultPlanHardLimits())
			if !hasValidationCode(errs, tt.code) {
				t.Fatalf("ValidatePlanProposal errors = %#v, want %q", errs, tt.code)
			}
		})
	}
}

func TestPlanProposalArtifactKindIsNotAValidTaskOutput(t *testing.T) {
	t.Parallel()

	plan := walkingSkeletonPlan()
	plan.Nodes[0].ArtifactKinds = []ArtifactKind{ArtifactKindPlanProposal}
	if errs := ValidatePlan(plan, "mission-1", DefaultPlanHardLimits()); !hasValidationCode(errs, "invalid_artifact_kind") {
		t.Fatalf("ValidatePlan errors = %#v, want invalid_artifact_kind", errs)
	}
}

func validPlanProposal() PlanProposal {
	return PlanProposal{
		SchemaVersion: PlanProposalSchemaVersion,
		MissionID:     "mission-1",
		ProposalKey:   "proposal-1",
		Input: PlanProposalInput{
			Objective: "Deliver the mission",
			ContextRefs: []PlanProposalContextRef{{
				Kind: "artifact", URI: "artifact://brief", ContentHash: "sha256:input",
			}},
			DeliveryCriteria: []string{"The integrated result passes its target gate"},
		},
		Limits: PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1},
		Nodes: []PlanProposalNode{
			proposalNode("A", DutyExecutor),
			proposalNode("B", DutyExecutor),
			proposalNode("C", DutyIntegrator, "A", "B"),
		},
	}
}

func proposalNode(key string, duty Duty, dependencies ...string) PlanProposalNode {
	kinds := []ArtifactKind{ArtifactKindCommit, ArtifactKindTestReceipt}
	if duty == DutyIntegrator {
		kinds = []ArtifactKind{ArtifactKindFinalDelivery}
	}
	return PlanProposalNode{
		Key: key, Title: "Task " + key, Description: "Deliver task " + key, Duty: duty,
		AcceptanceCriteria: []string{"Produces a verifiable result"}, ArtifactKinds: kinds,
		DependsOn: dependencies,
	}
}

func TestPlanProposalJSONUsesDutyNotLegacyRole(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(validPlanProposal())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"role"`)) || !bytes.Contains(encoded, []byte(`"duty":"executor"`)) {
		t.Fatalf("proposal JSON does not expose the duty contract: %s", encoded)
	}
}

package orchestration

import (
	"reflect"
	"testing"
	"time"
)

func routingTestSnapshot() RolePolicySnapshot {
	return RolePolicySnapshot{
		WorkspaceID: "workspace-1", SchemaVersion: RolePolicySnapshotSchemaVersion,
		Duty: DutyExecutor, RoleProfileKey: "go-executor", RoleProfileVersion: 2,
		Config: RoleProfileConfig{RequiredCapabilities: []string{"go", "linux"}, MaxConcurrency: 3,
			Runtime: RoleRuntimePreferences{PreferredRuntimeIDs: []string{"runtime-preferred"}},
		},
	}
}

func routingCandidate(id, runtime string, created time.Time) RoutingCandidateFacts {
	return RoutingCandidateFacts{
		AgentID: id, RuntimeID: runtime, AgentCreatedAt: created, AgentOwnerID: "owner",
		PermissionMode: "private", Model: "gpt", MaxConcurrentTasks: 4,
		RuntimeBound: true, RuntimeStatus: "online", Provider: "local",
		MetadataCapabilitiesKnown: true, MetadataCapabilities: []string{"go", "linux", "shell"},
		RuntimeOwnerPresent: true, RuntimeOwnerID: "owner", RuntimeVisibility: "private", CurrentLoad: 0,
	}
}

func TestSelectRoutingCandidateReusableForAllDuties(t *testing.T) {
	for _, duty := range []Duty{DutyPlanner, DutyExecutor, DutyReviewer, DutyIntegrator} {
		snapshot := routingTestSnapshot()
		snapshot.Duty = duty
		result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{routingCandidate("agent-1", "runtime-1", time.Unix(1, 0))}})
		if result.Selected == nil || result.Selected.AgentID != "agent-1" {
			t.Fatalf("duty %s selected = %+v", duty, result.Selected)
		}
	}
}

func TestSelectRoutingCandidateExplicitAvailableUnavailableMissing(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		s := routingTestSnapshot()
		s.AgentID = "agent-2"
		result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{routingCandidate("agent-2", "runtime-1", time.Unix(1, 0))}})
		if result.Selected == nil || result.Selected.AgentID != "agent-2" {
			t.Fatalf("selected = %+v", result.Selected)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		s := routingTestSnapshot()
		s.AgentID = "agent-2"
		c := routingCandidate("agent-2", "runtime-1", time.Unix(1, 0))
		c.Archived = true
		result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
		if result.Selected != nil || !hasReason(result.Evaluations[0], RoutingReasonExplicitBindingUnavailable) || !hasReason(result.Evaluations[0], RoutingReasonAgentArchived) {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("missing", func(t *testing.T) {
		s := routingTestSnapshot()
		s.AgentID = "agent-2"
		result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{routingCandidate("agent-1", "runtime-1", time.Unix(1, 0))}})
		if result.Selected != nil || len(result.Evaluations) != 1 || !hasReason(result.Evaluations[0], RoutingReasonExplicitBindingMissing) {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestSelectRoutingCandidatePermissionAndRuntimeFilters(t *testing.T) {
	s := routingTestSnapshot()
	c := routingCandidate("agent-1", "runtime-1", time.Unix(1, 0))
	c.AgentOwnerID = "other"
	c.PermissionMode = "private"
	c.RuntimeStatus = "offline"
	c.RuntimeOwnerPresent = false
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	for _, reason := range []string{RoutingReasonPermissionDenied, RoutingReasonRuntimeOffline, RoutingReasonRuntimeOwnerMissing} {
		if !hasReason(result.Evaluations[0], reason) {
			t.Errorf("missing %s: %+v", reason, result.Evaluations[0])
		}
	}
	c.PermissionMode = "public_to"
	c.WorkspaceGrant = true
	c.RuntimeStatus = "online"
	c.RuntimeOwnerPresent = true
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if result.Selected == nil {
		t.Fatalf("public workspace grant should pass: %+v", result)
	}
	c.WorkspaceGrant = false
	c.MemberGrant = true
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if result.Selected == nil {
		t.Fatalf("public member grant should pass: %+v", result)
	}
	c.MemberGrant = false
	c.RuntimeBound = false
	c.RuntimeOwnerPresent = false
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if hasReason(result.Evaluations[0], RoutingReasonRuntimeOwnerMissing) {
		t.Fatalf("unbound runtime must not report missing owner: %+v", result.Evaluations[0])
	}
}

func TestSelectRoutingCandidateRuntimeAccessFailClosed(t *testing.T) {
	snapshot := routingTestSnapshot()
	base := routingCandidate("agent-1", "runtime-1", time.Unix(1, 0))

	selfPrivate := base
	selfPrivate.RuntimeOwnerID = "owner"
	selfPrivate.RuntimeVisibility = "private"
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{selfPrivate}})
	if result.Selected == nil {
		t.Fatalf("self-owned private runtime should pass: %+v", result)
	}

	crossOwnerPublic := base
	crossOwnerPublic.RuntimeOwnerID = "other-owner"
	crossOwnerPublic.RuntimeVisibility = "public"
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{crossOwnerPublic}})
	if result.Selected == nil {
		t.Fatalf("cross-owner public runtime should pass: %+v", result)
	}

	crossOwnerPrivate := crossOwnerPublic
	crossOwnerPrivate.RuntimeVisibility = "private"
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{crossOwnerPrivate}})
	if result.Selected != nil || !hasReason(result.Evaluations[0], RoutingReasonRuntimePermissionDenied) {
		t.Fatalf("cross-owner private runtime should fail closed: %+v", result)
	}

	ownerless := base
	ownerless.RuntimeOwnerPresent = false
	ownerless.RuntimeOwnerID = ""
	ownerless.RuntimeVisibility = "public"
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{ownerless}})
	if result.Selected != nil || !hasReason(result.Evaluations[0], RoutingReasonRuntimeOwnerMissing) {
		t.Fatalf("ownerless runtime should fail closed: %+v", result)
	}

	snapshot.AgentID = ownerless.AgentID
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{ownerless}})
	if result.Selected != nil || !hasReason(result.Evaluations[0], RoutingReasonExplicitBindingUnavailable) {
		t.Fatalf("explicit binding must not bypass runtime access: %+v", result)
	}
}

func TestSelectRoutingCandidateExactRuntimeAndCapabilitySubset(t *testing.T) {
	s := routingTestSnapshot()
	s.Config.Runtime.AllowedRuntimeIDs = []string{"runtime-allowed"}
	s.Config.Runtime.Providers = []string{"provider-a"}
	s.Config.Runtime.Models = []string{"model-a"}
	c := routingCandidate("agent-1", "runtime-wrong", time.Unix(1, 0))
	c.Provider = "provider-b"
	c.Model = "model-b"
	c.MetadataCapabilities = []string{"go"}
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	for _, reason := range []string{RoutingReasonRuntimeNotAllowed, RoutingReasonProviderNotAllowed, RoutingReasonModelNotAllowed, RoutingReasonCapabilityMissing} {
		if !hasReason(result.Evaluations[0], reason) {
			t.Errorf("missing %s: %+v", reason, result.Evaluations[0])
		}
	}
	c.RuntimeID = "runtime-allowed"
	c.Provider = "provider-a"
	c.Model = "model-a"
	c.MetadataCapabilities = []string{"go", "linux"}
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if result.Selected == nil {
		t.Fatalf("exact matching facts should pass: %+v", result)
	}
}

func TestSelectRoutingCandidateCapabilitiesToolPathAndCapacityFailClosed(t *testing.T) {
	s := routingTestSnapshot()
	s.Config.Tools.AllowedTools = []string{"shell"}
	s.Config.Tools.AllowedPaths = []string{"/repo"}
	c := routingCandidate("agent-1", "runtime-1", time.Unix(1, 0))
	c.MetadataCapabilitiesKnown = false
	c.CurrentLoad = 3
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	for _, reason := range []string{RoutingReasonCapabilityUnknown, RoutingReasonToolPolicyUnsupported, RoutingReasonPathPolicyUnsupported, RoutingReasonCapacityExhausted} {
		if !hasReason(result.Evaluations[0], reason) {
			t.Errorf("missing %s: %+v", reason, result.Evaluations[0])
		}
	}
	c.MetadataCapabilitiesKnown = true
	c.MetadataCapabilitiesMalformed = true
	c.CurrentLoad = 0
	s.Config.Tools.AllowedTools = nil
	s.Config.Tools.AllowedPaths = nil
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if !hasReason(result.Evaluations[0], RoutingReasonCapabilityMalformed) {
		t.Fatalf("malformed capabilities should fail closed: %+v", result)
	}
}

func TestSelectRoutingCandidateDeduplicatesNormalizedCapabilities(t *testing.T) {
	s := routingTestSnapshot()
	c := routingCandidate("agent-1", "runtime-1", time.Unix(1, 0))
	c.MetadataCapabilities = []string{"go", "linux", "go", "linux"}
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if result.Selected == nil || hasReason(result.Evaluations[0], RoutingReasonCapabilityMalformed) {
		t.Fatalf("duplicate normalized capabilities should pass: %+v", result)
	}
	c.MetadataCapabilities = []string{"go", ""}
	result = SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: []RoutingCandidateFacts{c}})
	if result.Selected != nil || !hasReason(result.Evaluations[0], RoutingReasonCapabilityMalformed) {
		t.Fatalf("empty capability should fail closed: %+v", result)
	}
}

func TestSelectRoutingCandidateStableRankingAndProducerPreference(t *testing.T) {
	s := routingTestSnapshot()
	base := time.Unix(100, 0)
	producer := routingCandidate("producer", "runtime-other", base.Add(-time.Hour))
	producer.CurrentLoad = 0
	preferred := routingCandidate("preferred", "runtime-preferred", base)
	preferred.CurrentLoad = 2
	older := routingCandidate("older", "runtime-other", base.Add(-time.Minute))
	older.CurrentLoad = 1
	in := RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", ProducerAgentID: "producer", Candidates: []RoutingCandidateFacts{producer, preferred, older}}
	first := SelectRoutingCandidate(in)
	second := SelectRoutingCandidate(in)
	if first.Selected == nil || first.Selected.AgentID != "preferred" {
		t.Fatalf("ranking selected = %+v", first.Selected)
	}
	if first.EvidenceHash != second.EvidenceHash || !reflect.DeepEqual(first.Evaluations, second.Evaluations) {
		t.Fatal("selection evidence is not stable")
	}
	for _, evaluation := range first.Evaluations {
		if !evaluation.Eligible {
			t.Fatalf("all hard-filter-passing candidates must remain eligible: %+v", first.Evaluations)
		}
	}
	producerRef := routingCandidateRef(s, "producer", "runtime-other")
	if first.Evaluations[len(first.Evaluations)-1].CandidateRef != producerRef || first.Evaluations[len(first.Evaluations)-1].SeparationPreferred {
		t.Fatalf("producer should be ordered last with separation preference recorded: %+v", first.Evaluations)
	}
}

func TestSelectRoutingCandidateReviewerProducerHardSeparation(t *testing.T) {
	snapshot := routingTestSnapshot()
	snapshot.Duty = DutyReviewer
	producer := routingCandidate("producer", "runtime-producer", time.Unix(1, 0))
	independent := routingCandidate("independent", "runtime-independent", time.Unix(2, 0))
	input := RoutingSelectionInput{
		Snapshot: snapshot, EffectiveOwnerID: "owner", ProducerAgentID: producer.AgentID,
		Candidates: []RoutingCandidateFacts{producer, independent},
	}
	result := SelectRoutingCandidate(input)
	if result.Selected == nil || result.Selected.AgentID != independent.AgentID {
		t.Fatalf("reviewer should select independent candidate: %+v", result)
	}
	for _, evaluation := range result.Evaluations {
		if evaluation.CandidateRef == routingCandidateRef(snapshot, producer.AgentID, producer.RuntimeID) {
			if evaluation.Eligible || !hasReason(evaluation, RoutingReasonReviewerIsProducer) {
				t.Fatalf("producer must be hard-rejected: %+v", evaluation)
			}
		}
	}

	result = SelectRoutingCandidate(RoutingSelectionInput{
		Snapshot: snapshot, EffectiveOwnerID: "owner", ProducerAgentID: producer.AgentID,
		Candidates: []RoutingCandidateFacts{producer},
	})
	if result.Selected != nil || len(result.Evaluations) != 1 || !hasReason(result.Evaluations[0], RoutingReasonReviewerIsProducer) {
		t.Fatalf("only producer must block reviewer selection: %+v", result)
	}
}

func TestSelectRoutingCandidateReviewerExplicitProducerCannotBypassSeparation(t *testing.T) {
	snapshot := routingTestSnapshot()
	snapshot.Duty = DutyReviewer
	snapshot.AgentID = "producer"
	producer := routingCandidate("producer", "runtime-producer", time.Unix(1, 0))
	result := SelectRoutingCandidate(RoutingSelectionInput{
		Snapshot: snapshot, EffectiveOwnerID: "owner", ProducerAgentID: producer.AgentID,
		Candidates: []RoutingCandidateFacts{producer},
	})
	if result.Selected != nil || !hasReason(result.Evaluations[0], RoutingReasonReviewerIsProducer) || !hasReason(result.Evaluations[0], RoutingReasonExplicitBindingUnavailable) {
		t.Fatalf("explicit producer binding must fail closed: %+v", result)
	}
}

func TestSelectRoutingCandidateProducerSeparationOnlyAppliesToReviewerDuty(t *testing.T) {
	for _, duty := range []Duty{DutyPlanner, DutyExecutor, DutyIntegrator} {
		snapshot := routingTestSnapshot()
		snapshot.Duty = duty
		producer := routingCandidate("producer", "runtime-producer", time.Unix(1, 0))
		result := SelectRoutingCandidate(RoutingSelectionInput{
			Snapshot: snapshot, EffectiveOwnerID: "owner", ProducerAgentID: producer.AgentID,
			Candidates: []RoutingCandidateFacts{producer},
		})
		if result.Selected == nil || result.Selected.AgentID != producer.AgentID {
			t.Fatalf("duty %s must retain non-reviewer producer eligibility: %+v", duty, result)
		}
		if hasReason(result.Evaluations[0], RoutingReasonReviewerIsProducer) {
			t.Fatalf("duty %s unexpectedly applied reviewer separation: %+v", duty, result.Evaluations[0])
		}
	}
	// AvoidAgentID alone remains a soft preference even for a reviewer.
	snapshot := routingTestSnapshot()
	snapshot.Duty = DutyReviewer
	avoid := routingCandidate("avoid", "runtime-avoid", time.Unix(1, 0))
	result := SelectRoutingCandidate(RoutingSelectionInput{
		Snapshot: snapshot, EffectiveOwnerID: "owner", AvoidAgentID: avoid.AgentID,
		Candidates: []RoutingCandidateFacts{avoid},
	})
	if result.Selected == nil || hasReason(result.Evaluations[0], RoutingReasonReviewerIsProducer) {
		t.Fatalf("avoid-only reviewer input must remain soft: %+v", result)
	}
}

func TestRoutingHashesDoNotExposeCandidateIDs(t *testing.T) {
	s := routingTestSnapshot()
	s.AgentID = "secret-agent"
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: nil})
	if result.EvidenceHash == "" || result.SnapshotHash == "" {
		t.Fatal("missing hashes")
	}
	if result.Evaluations[0].CandidateRef == "secret-agent" || result.Evaluations[0].CandidateRef == "" {
		t.Fatalf("candidate ref leaked or missing: %+v", result.Evaluations[0])
	}
	if len(result.Evaluations[0].CandidateRef) < len("candidate_")+32 {
		t.Fatalf("candidate ref must contain at least 128 bits: %q", result.Evaluations[0].CandidateRef)
	}
}

func TestRoutingSnapshotHashUsesFrozenContentHash(t *testing.T) {
	s := routingTestSnapshot()
	s.ContentHash = "frozen-content-hash"
	result := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: s, EffectiveOwnerID: "owner", Candidates: nil})
	if result.SnapshotHash != s.ContentHash {
		t.Fatalf("snapshot hash = %q, want frozen content hash", result.SnapshotHash)
	}
}

func hasReason(e RoutingCandidateEvaluation, reason string) bool {
	for _, code := range e.ReasonCodes {
		if code == reason {
			return true
		}
	}
	return false
}

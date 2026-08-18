package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/kailonyang/liexiu/server/internal/agentaccess"
)

// RoutingSelectionVersion identifies the deterministic ordering and reason
// vocabulary used by this pure selector. It is part of the evidence contract.
const RoutingSelectionVersion = "4b5.v1"

// Stable routing rejection codes. Their order in RoutingCandidateEvaluation is
// the order below; callers must not replace these with free-form explanations.
const (
	RoutingReasonExplicitBindingUnavailable = "explicit_binding_unavailable"
	RoutingReasonExplicitBindingMissing     = "explicit_binding_missing"
	RoutingReasonAgentArchived              = "agent_archived"
	RoutingReasonPermissionDenied           = "permission_denied"
	RoutingReasonRuntimeUnbound             = "runtime_unbound"
	RoutingReasonRuntimeOffline             = "runtime_offline"
	RoutingReasonRuntimeOwnerMissing        = "runtime_owner_missing"
	RoutingReasonRuntimeNotAllowed          = "runtime_not_allowed"
	RoutingReasonProviderNotAllowed         = "provider_not_allowed"
	RoutingReasonModelNotAllowed            = "model_not_allowed"
	RoutingReasonCapabilityUnknown          = "capability_unknown"
	RoutingReasonCapabilityMalformed        = "capability_malformed"
	RoutingReasonCapabilityMissing          = "capability_missing"
	RoutingReasonToolPolicyUnsupported      = "tool_policy_unsupported"
	RoutingReasonPathPolicyUnsupported      = "path_policy_unsupported"
	RoutingReasonCapacityExhausted          = "capacity_exhausted"
	RoutingReasonInvalidCapacity            = "invalid_capacity_limit"
	RoutingReasonReviewerIsProducer         = "reviewer_is_producer"
	RoutingReasonRuntimePermissionDenied    = "runtime_permission_denied"
)

// RoutingCandidateFacts is the smallest server-side fact set needed by v1.
// It deliberately contains no generated DB types and no display names.
type RoutingCandidateFacts struct {
	AgentID                       string
	RuntimeID                     string
	AgentCreatedAt                time.Time
	Archived                      bool
	AgentOwnerID                  string
	PermissionMode                string
	WorkspaceGrant                bool
	MemberGrant                   bool
	Model                         string
	MaxConcurrentTasks            int
	RuntimeBound                  bool
	RuntimeStatus                 string
	Provider                      string
	MetadataCapabilitiesKnown     bool
	MetadataCapabilities          []string
	MetadataCapabilitiesMalformed bool
	RuntimeOwnerPresent           bool
	RuntimeOwnerID                string
	RuntimeVisibility             string
	CurrentLoad                   int
}

// RoutingSelectionInput is immutable input to SelectRoutingCandidate. The
// snapshot is already frozen; the selector never consults RoleProfile state.
type RoutingSelectionInput struct {
	Snapshot         RolePolicySnapshot
	EffectiveOwnerID string
	Candidates       []RoutingCandidateFacts
	ProducerAgentID  string
	AvoidAgentID     string
}

type RoutingSelectedCandidate struct {
	AgentID   string `json:"agent_id"`
	RuntimeID string `json:"runtime_id"`
}

type RoutingCandidateEvaluation struct {
	CandidateRef         string   `json:"candidate_ref"`
	Eligible             bool     `json:"eligible"`
	ReasonCodes          []string `json:"reason_codes"`
	SeparationPreferred  bool     `json:"separation_preferred"`
	PreferredRuntimeRank int      `json:"preferred_runtime_rank"`
	CurrentLoad          int      `json:"current_load"`
}

type RoutingSelectionResult struct {
	SelectionVersion string                       `json:"selection_version"`
	SnapshotHash     string                       `json:"snapshot_hash"`
	Selected         *RoutingSelectedCandidate    `json:"selected,omitempty"`
	Evaluations      []RoutingCandidateEvaluation `json:"evaluations"`
	EvidenceHash     string                       `json:"evidence_hash"`
}

// SelectRoutingCandidate applies all v1 hard filters and then stable ranking.
// It is intentionally a pure function: no database access, current profile
// lookup, permission side channel, or mutation is possible here.
func SelectRoutingCandidate(input RoutingSelectionInput) RoutingSelectionResult {
	result := RoutingSelectionResult{SelectionVersion: RoutingSelectionVersion}
	result.SnapshotHash = routingSnapshotHash(input.Snapshot)

	explicit := strings.TrimSpace(input.Snapshot.AgentID)
	matchedExplicit := false
	type evaluated struct {
		facts RoutingCandidateFacts
		eval  RoutingCandidateEvaluation
		ok    bool
	}
	evaluatedCandidates := make([]evaluated, 0, len(input.Candidates))
	for _, facts := range input.Candidates {
		if explicit != "" && facts.AgentID != explicit {
			continue
		}
		if explicit != "" {
			matchedExplicit = true
		}
		e := evaluateRoutingCandidate(input, facts)
		evaluatedCandidates = append(evaluatedCandidates, evaluated{facts: facts, eval: e.eval, ok: len(e.eval.ReasonCodes) == 0})
	}
	if explicit != "" && !matchedExplicit {
		result.Evaluations = append(result.Evaluations, RoutingCandidateEvaluation{
			CandidateRef: routingCandidateRef(input.Snapshot, explicit, ""),
			ReasonCodes:  []string{RoutingReasonExplicitBindingMissing},
		})
		return finalizeRoutingEvidence(result)
	}

	if explicit != "" && matchedExplicit {
		for i := range evaluatedCandidates {
			if len(evaluatedCandidates[i].eval.ReasonCodes) > 0 {
				evaluatedCandidates[i].eval.ReasonCodes = append([]string{RoutingReasonExplicitBindingUnavailable}, evaluatedCandidates[i].eval.ReasonCodes...)
			}
		}
	}
	eligible := make([]evaluated, 0, len(evaluatedCandidates))
	for _, item := range evaluatedCandidates {
		if len(item.eval.ReasonCodes) == 0 {
			eligible = append(eligible, item)
		}
	}

	separationID := strings.TrimSpace(input.ProducerAgentID)
	if separationID == "" {
		separationID = strings.TrimSpace(input.AvoidAgentID)
	}
	for i := range eligible {
		eligible[i].eval.SeparationPreferred = separationID != "" && eligible[i].facts.AgentID != separationID
		eligible[i].eval.PreferredRuntimeRank = preferredRuntimeRank(input.Snapshot.Config.Runtime.PreferredRuntimeIDs, eligible[i].facts.RuntimeID)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		ap, bp := eligible[i].eval.SeparationPreferred, eligible[j].eval.SeparationPreferred
		if ap != bp {
			return ap
		}
		if a.eval.PreferredRuntimeRank != b.eval.PreferredRuntimeRank {
			return a.eval.PreferredRuntimeRank < b.eval.PreferredRuntimeRank
		}
		if a.facts.CurrentLoad != b.facts.CurrentLoad {
			return a.facts.CurrentLoad < b.facts.CurrentLoad
		}
		if !a.facts.AgentCreatedAt.Equal(b.facts.AgentCreatedAt) {
			return a.facts.AgentCreatedAt.Before(b.facts.AgentCreatedAt)
		}
		return a.facts.AgentID < b.facts.AgentID
	})
	for i := range eligible {
		// Eligible means that every hard filter passed. Selection is represented
		// separately by result.Selected; a lower-ranked eligible candidate must
		// remain auditable as eligible rather than being marked rejected.
		eligible[i].eval.Eligible = true
	}
	if len(eligible) > 0 {
		result.Selected = &RoutingSelectedCandidate{AgentID: eligible[0].facts.AgentID, RuntimeID: eligible[0].facts.RuntimeID}
	}

	// Eligible candidates are emitted in selection order; rejected candidates
	// follow in pseudonymous reference order. This makes the evidence complete
	// while keeping all hard-filter reasons stable.
	for _, item := range eligible {
		result.Evaluations = append(result.Evaluations, item.eval)
	}
	rejected := make([]evaluated, 0, len(evaluatedCandidates))
	for _, item := range evaluatedCandidates {
		if !item.ok {
			rejected = append(rejected, item)
		}
	}
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].eval.CandidateRef < rejected[j].eval.CandidateRef })
	for _, item := range rejected {
		result.Evaluations = append(result.Evaluations, item.eval)
	}
	return finalizeRoutingEvidence(result)
}

func evaluateRoutingCandidate(input RoutingSelectionInput, facts RoutingCandidateFacts) (out struct{ eval RoutingCandidateEvaluation }) {
	out.eval.CandidateRef = routingCandidateRef(input.Snapshot, facts.AgentID, facts.RuntimeID)
	add := func(reason string) { out.eval.ReasonCodes = append(out.eval.ReasonCodes, reason) }
	if facts.Archived {
		add(RoutingReasonAgentArchived)
	}
	if input.Snapshot.Duty == DutyReviewer && strings.TrimSpace(input.ProducerAgentID) != "" && facts.AgentID == strings.TrimSpace(input.ProducerAgentID) {
		add(RoutingReasonReviewerIsProducer)
	}
	targets := make([]agentaccess.Target, 0, 2)
	if facts.WorkspaceGrant {
		targets = append(targets, agentaccess.Target{Type: "workspace"})
	}
	if facts.MemberGrant {
		targets = append(targets, agentaccess.Target{Type: "member", ID: input.EffectiveOwnerID})
	}
	if !agentaccess.CanInvoke(
		agentaccess.Principal{ActorType: "member", ActorID: input.EffectiveOwnerID},
		agentaccess.GrantFacts{OwnerID: facts.AgentOwnerID, PermissionMode: facts.PermissionMode, IsWorkspaceMember: facts.WorkspaceGrant},
		targets,
	) {
		add(RoutingReasonPermissionDenied)
	}
	if !facts.RuntimeBound {
		add(RoutingReasonRuntimeUnbound)
	}
	if facts.RuntimeBound && facts.RuntimeStatus != "online" {
		add(RoutingReasonRuntimeOffline)
	}
	if facts.RuntimeBound && !facts.RuntimeOwnerPresent {
		add(RoutingReasonRuntimeOwnerMissing)
	} else if facts.RuntimeBound && strings.TrimSpace(facts.RuntimeOwnerID) == "" {
		add(RoutingReasonRuntimeOwnerMissing)
	} else if facts.RuntimeBound && facts.RuntimeOwnerID != input.EffectiveOwnerID && facts.RuntimeVisibility != "public" {
		add(RoutingReasonRuntimePermissionDenied)
	}
	config := input.Snapshot.Config
	if len(config.Runtime.AllowedRuntimeIDs) > 0 && !contains(config.Runtime.AllowedRuntimeIDs, facts.RuntimeID) {
		add(RoutingReasonRuntimeNotAllowed)
	}
	if len(config.Runtime.Providers) > 0 && !contains(config.Runtime.Providers, facts.Provider) {
		add(RoutingReasonProviderNotAllowed)
	}
	if len(config.Runtime.Models) > 0 && !contains(config.Runtime.Models, facts.Model) {
		add(RoutingReasonModelNotAllowed)
	}
	if len(config.RequiredCapabilities) > 0 {
		if !facts.MetadataCapabilitiesKnown {
			add(RoutingReasonCapabilityUnknown)
		} else if facts.MetadataCapabilitiesMalformed || capabilityValuesMalformed(facts.MetadataCapabilities) {
			add(RoutingReasonCapabilityMalformed)
		} else if !subset(config.RequiredCapabilities, normalizeCapabilityValues(facts.MetadataCapabilities)) {
			add(RoutingReasonCapabilityMissing)
		}
	}
	if len(config.Tools.AllowedTools) > 0 {
		add(RoutingReasonToolPolicyUnsupported)
	}
	if len(config.Tools.AllowedPaths) > 0 {
		add(RoutingReasonPathPolicyUnsupported)
	}
	limit := facts.MaxConcurrentTasks
	if limit <= 0 || config.MaxConcurrency <= 0 {
		add(RoutingReasonInvalidCapacity)
	} else {
		if config.MaxConcurrency < limit {
			limit = config.MaxConcurrency
		}
		if facts.CurrentLoad >= limit {
			add(RoutingReasonCapacityExhausted)
		}
	}
	out.eval.CurrentLoad = facts.CurrentLoad
	return out
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func subset(required, available []string) bool {
	for _, item := range required {
		if !contains(available, item) {
			return false
		}
	}
	return true
}

func capabilityValuesMalformed(values []string) bool {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return true
		}
	}
	return false
}

func normalizeCapabilityValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
func preferredRuntimeRank(preferred []string, runtimeID string) int {
	for i, id := range preferred {
		if id == runtimeID {
			return i
		}
	}
	return len(preferred) + 1
}

func routingCandidateRef(snapshot RolePolicySnapshot, agentID, runtimeID string) string {
	sum := sha256.Sum256([]byte(snapshot.WorkspaceID + "\x00" + agentID + "\x00" + runtimeID))
	return "candidate_" + hex.EncodeToString(sum[:16])
}

func routingSnapshotHash(snapshot RolePolicySnapshot) string {
	if strings.TrimSpace(snapshot.ContentHash) != "" {
		return snapshot.ContentHash
	}
	payload := struct {
		SchemaVersion int32             `json:"schema_version"`
		Duty          Duty              `json:"duty"`
		ProfileKey    string            `json:"profile_key"`
		Version       int32             `json:"version"`
		Config        RoleProfileConfig `json:"config"`
		AgentID       string            `json:"agent_id,omitempty"`
	}{snapshot.SchemaVersion, snapshot.Duty, snapshot.RoleProfileKey, snapshot.RoleProfileVersion, snapshot.Config, snapshot.AgentID}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func finalizeRoutingEvidence(result RoutingSelectionResult) RoutingSelectionResult {
	payload := struct {
		Version      string                       `json:"version"`
		SnapshotHash string                       `json:"snapshot_hash"`
		Selected     *RoutingSelectedCandidate    `json:"selected,omitempty"`
		Evaluations  []RoutingCandidateEvaluation `json:"evaluations"`
	}{result.SelectionVersion, result.SnapshotHash, result.Selected, result.Evaluations}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	result.EvidenceHash = hex.EncodeToString(sum[:])
	return result
}

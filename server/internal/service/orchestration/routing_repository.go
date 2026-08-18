package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// routingFactsQuerier is deliberately small so the selection/recheck loop can
// be tested without a database and can be used by both pooled and transactional
// generated query handles.
type routingFactsQuerier interface {
	ListOrchestrationAgentRoutingFacts(context.Context, db.ListOrchestrationAgentRoutingFactsParams) ([]db.ListOrchestrationAgentRoutingFactsRow, error)
	LockOrchestrationAgentRoutingFacts(context.Context, db.LockOrchestrationAgentRoutingFactsParams) (db.LockOrchestrationAgentRoutingFactsRow, error)
}

// ErrRoutingSelectionUnstable means the candidate set kept changing during the
// bounded lock/re-list loop. The caller must leave the work unassigned.
var ErrRoutingSelectionUnstable = errors.New("routing candidate set did not stabilize")

type routingMetadataCapabilities struct {
	Known     bool
	Values    []string
	Malformed bool
}

// parseRoutingMetadataCapabilities fails closed for malformed metadata while
// treating a missing/null capabilities field as unknown (rather than empty).
func parseRoutingMetadataCapabilities(raw []byte) routingMetadataCapabilities {
	if len(raw) == 0 || string(raw) == "null" {
		return routingMetadataCapabilities{}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return routingMetadataCapabilities{Known: true, Malformed: true}
	}
	encoded, ok := object["capabilities"]
	if !ok || string(encoded) == "null" {
		return routingMetadataCapabilities{}
	}
	var values []json.RawMessage
	if err := json.Unmarshal(encoded, &values); err != nil {
		return routingMetadataCapabilities{Known: true, Malformed: true}
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, item := range values {
		var value string
		if err := json.Unmarshal(item, &value); err != nil || strings.TrimSpace(value) == "" {
			return routingMetadataCapabilities{Known: true, Malformed: true}
		}
		value = strings.TrimSpace(value)
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return routingMetadataCapabilities{Known: true, Values: result}
}

func routingCandidateFactsFromListRow(row db.ListOrchestrationAgentRoutingFactsRow) RoutingCandidateFacts {
	capabilities := parseRoutingMetadataCapabilities(row.RuntimeMetadata)
	return RoutingCandidateFacts{
		AgentID: uuidText(row.AgentID), RuntimeID: uuidText(row.RuntimeID),
		AgentCreatedAt: row.AgentCreatedAt.Time, Archived: row.ArchivedAt.Valid,
		AgentOwnerID: uuidText(row.AgentOwnerID), PermissionMode: row.PermissionMode,
		WorkspaceGrant: row.HasWorkspaceInvocationTarget, MemberGrant: row.HasMemberInvocationTarget,
		Model: row.Model.String, MaxConcurrentTasks: int(row.MaxConcurrentTasks),
		RuntimeBound: row.RuntimeID.Valid, RuntimeStatus: row.RuntimeStatus.String,
		Provider: row.RuntimeProvider.String, MetadataCapabilitiesKnown: capabilities.Known,
		MetadataCapabilities: capabilities.Values, MetadataCapabilitiesMalformed: capabilities.Malformed,
		RuntimeOwnerPresent: row.RuntimeOwnerID.Valid, RuntimeOwnerID: uuidText(row.RuntimeOwnerID), RuntimeVisibility: row.RuntimeVisibility.String,
		CurrentLoad: int(row.CurrentLoad),
	}
}

func routingCandidateFactsFromLockRow(row db.LockOrchestrationAgentRoutingFactsRow) RoutingCandidateFacts {
	capabilities := parseRoutingMetadataCapabilities(row.RuntimeMetadata)
	return RoutingCandidateFacts{
		AgentID: uuidText(row.AgentID), RuntimeID: uuidText(row.RuntimeID),
		AgentCreatedAt: row.AgentCreatedAt.Time, Archived: row.ArchivedAt.Valid,
		AgentOwnerID: uuidText(row.AgentOwnerID), PermissionMode: row.PermissionMode,
		WorkspaceGrant: row.HasWorkspaceInvocationTarget, MemberGrant: row.HasMemberInvocationTarget,
		Model: row.Model.String, MaxConcurrentTasks: int(row.MaxConcurrentTasks),
		RuntimeBound: row.RuntimeID.Valid, RuntimeStatus: row.RuntimeStatus,
		Provider: row.RuntimeProvider, MetadataCapabilitiesKnown: capabilities.Known,
		MetadataCapabilities: capabilities.Values, MetadataCapabilitiesMalformed: capabilities.Malformed,
		RuntimeOwnerPresent: row.RuntimeOwnerID.Valid, RuntimeOwnerID: uuidText(row.RuntimeOwnerID), RuntimeVisibility: row.RuntimeVisibility,
		CurrentLoad: int(row.CurrentLoad),
	}
}

func selectAndLockRoutingCandidate(ctx context.Context, q routingFactsQuerier, workspaceID, effectiveUserID pgtype.UUID, snapshot RolePolicySnapshot, producerAgentID, avoidAgentID string) (RoutingSelectionResult, error) {
	params := db.ListOrchestrationAgentRoutingFactsParams{WorkspaceID: workspaceID, EffectiveUserID: effectiveUserID}
	rows, err := q.ListOrchestrationAgentRoutingFacts(ctx, params)
	if err != nil {
		return RoutingSelectionResult{}, fmt.Errorf("list routing candidates: %w", err)
	}
	maxAttempts := len(rows) + 2
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	var last RoutingSelectionResult
	for attempt := 0; attempt < maxAttempts; attempt++ {
		facts := make([]RoutingCandidateFacts, 0, len(rows))
		for _, row := range rows {
			facts = append(facts, routingCandidateFactsFromListRow(row))
		}
		input := RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: uuidText(effectiveUserID), Candidates: facts, ProducerAgentID: producerAgentID, AvoidAgentID: avoidAgentID}
		selected := SelectRoutingCandidate(input)
		last = selected
		if selected.Selected == nil {
			return selected, nil
		}
		locked, lockErr := q.LockOrchestrationAgentRoutingFacts(ctx, db.LockOrchestrationAgentRoutingFactsParams{WorkspaceID: workspaceID, EffectiveUserID: effectiveUserID, AgentID: mustParseUUID(selected.Selected.AgentID)})
		if errors.Is(lockErr, pgx.ErrNoRows) {
			rows, err = q.ListOrchestrationAgentRoutingFacts(ctx, params)
			if err != nil {
				return RoutingSelectionResult{}, fmt.Errorf("re-list routing candidates after lock race: %w", err)
			}
			continue
		}
		if lockErr != nil {
			return RoutingSelectionResult{}, fmt.Errorf("lock routing candidate: %w", lockErr)
		}
		// Re-list after the lock. This captures the locked row's authoritative
		// load and any concurrent eligibility changes visible in this transaction.
		rows, err = q.ListOrchestrationAgentRoutingFacts(ctx, params)
		if err != nil {
			return RoutingSelectionResult{}, fmt.Errorf("re-list routing candidates after lock: %w", err)
		}
		postFacts := make([]RoutingCandidateFacts, 0, len(rows)+1)
		for _, row := range rows {
			postFacts = append(postFacts, routingCandidateFactsFromListRow(row))
		}
		// Some fakes (and unusual query wrappers) return the locked read without
		// reflecting it in a subsequent list; retain that authoritative fact.
		lockedFacts := routingCandidateFactsFromLockRow(locked)
		found := false
		for i := range postFacts {
			if postFacts[i].AgentID == lockedFacts.AgentID {
				postFacts[i] = lockedFacts
				found = true
				break
			}
		}
		if !found {
			postFacts = append(postFacts, lockedFacts)
		}
		post := SelectRoutingCandidate(RoutingSelectionInput{Snapshot: snapshot, EffectiveOwnerID: uuidText(effectiveUserID), Candidates: postFacts, ProducerAgentID: producerAgentID, AvoidAgentID: avoidAgentID})
		last = post
		if post.Selected != nil && post.Selected.AgentID == selected.Selected.AgentID && post.Selected.RuntimeID == selected.Selected.RuntimeID {
			return post, nil
		}
		rows = make([]db.ListOrchestrationAgentRoutingFactsRow, 0, len(postFacts))
		// The next iteration must use fresh generated rows. Re-list rather than
		// fabricating nullable DB rows from pure facts.
		rows, err = q.ListOrchestrationAgentRoutingFacts(ctx, params)
		if err != nil {
			return RoutingSelectionResult{}, fmt.Errorf("re-list routing candidates after re-selection: %w", err)
		}
	}
	return last, ErrRoutingSelectionUnstable
}

func mustParseUUID(value string) pgtype.UUID {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

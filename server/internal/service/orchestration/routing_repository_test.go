package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestParseRoutingMetadataCapabilities(t *testing.T) {
	tests := []struct {
		name             string
		raw              string
		known, malformed bool
		values           []string
	}{
		{"missing", ``, false, false, nil},
		{"null", `null`, false, false, nil},
		{"valid and normalized", `{"capabilities":[" z ","a","a"]}`, true, false, []string{"a", "z"}},
		{"empty array", `{"capabilities":[]}`, true, false, []string{}},
		{"wrong type", `{"capabilities":"shell"}`, true, true, nil},
		{"non string", `{"capabilities":["shell",3]}`, true, true, nil},
		{"blank", `{"capabilities":["shell"," "]}`, true, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRoutingMetadataCapabilities([]byte(tt.raw))
			if got.Known != tt.known || got.Malformed != tt.malformed {
				t.Fatalf("metadata=%#v, want known=%v malformed=%v", got, tt.known, tt.malformed)
			}
			if len(got.Values) != len(tt.values) {
				t.Fatalf("values=%v, want %v", got.Values, tt.values)
			}
			for i := range got.Values {
				if got.Values[i] != tt.values[i] {
					t.Fatalf("values=%v, want %v", got.Values, tt.values)
				}
			}
		})
	}
}

func TestRoutingCandidateFactsFromListRow(t *testing.T) {
	agentID := testRoutingUUID(1)
	runtimeID := testRoutingUUID(2)
	row := db.ListOrchestrationAgentRoutingFactsRow{
		AgentID: agentID, RuntimeID: runtimeID,
		AgentCreatedAt: pgtype.Timestamptz{Time: time.Unix(10, 0), Valid: true},
		ArchivedAt:     pgtype.Timestamptz{Valid: true}, AgentOwnerID: testRoutingUUID(3),
		PermissionMode: "public_to_workspace", WorkspaceID: testRoutingUUID(4),
		Model: pgtype.Text{String: "m", Valid: true}, MaxConcurrentTasks: 4,
		RuntimeStatus: pgtype.Text{String: "online", Valid: true}, RuntimeProvider: pgtype.Text{String: "p", Valid: true},
		RuntimeMetadata: []byte(`{"capabilities":[" b ","a","a"]}`), RuntimeOwnerID: testRoutingUUID(5),
		RuntimeVisibility: pgtype.Text{String: "private", Valid: true}, CurrentLoad: 2,
		HasWorkspaceInvocationTarget: true, HasMemberInvocationTarget: true,
	}
	facts := routingCandidateFactsFromListRow(row)
	if facts.AgentID != uuidText(agentID) || facts.RuntimeID != uuidText(runtimeID) || !facts.Archived || !facts.RuntimeBound || !facts.RuntimeOwnerPresent || facts.RuntimeOwnerID != uuidText(testRoutingUUID(5)) {
		t.Fatalf("identity/null mapping incorrect: %#v", facts)
	}
	if facts.MetadataCapabilitiesKnown != true || facts.MetadataCapabilitiesMalformed || len(facts.MetadataCapabilities) != 2 || facts.MetadataCapabilities[0] != "a" {
		t.Fatalf("capability mapping incorrect: %#v", facts)
	}
	if facts.CurrentLoad != 2 || facts.MaxConcurrentTasks != 4 || !facts.WorkspaceGrant || !facts.MemberGrant {
		t.Fatalf("policy/load mapping incorrect: %#v", facts)
	}
}

func TestRoutingCandidateFactsFromLockRowMapsRuntimeAccess(t *testing.T) {
	agentID := testRoutingUUID(11)
	runtimeID := testRoutingUUID(12)
	ownerID := testRoutingUUID(13)
	row := db.LockOrchestrationAgentRoutingFactsRow{
		AgentID: agentID, RuntimeID: runtimeID,
		AgentCreatedAt: pgtype.Timestamptz{Time: time.Unix(10, 0), Valid: true},
		AgentOwnerID:   agentID, PermissionMode: "private", Model: pgtype.Text{String: "m", Valid: true}, MaxConcurrentTasks: 2,
		RuntimeStatus: "online", RuntimeProvider: "p", RuntimeOwnerID: ownerID, RuntimeVisibility: "public",
		RuntimeMetadata: []byte(`{"capabilities":[]}`), CurrentLoad: 1,
	}
	facts := routingCandidateFactsFromLockRow(row)
	if !facts.RuntimeBound || !facts.RuntimeOwnerPresent || facts.RuntimeOwnerID != uuidText(ownerID) || facts.RuntimeVisibility != "public" {
		t.Fatalf("lock-row runtime access mapping incorrect: %#v", facts)
	}
}

type routingFactsFake struct {
	lists                [][]db.ListOrchestrationAgentRoutingFactsRow
	locks                []routingFactsLockResult
	listCalls, lockCalls int
}
type routingFactsLockResult struct {
	row db.LockOrchestrationAgentRoutingFactsRow
	err error
}

func (f *routingFactsFake) ListOrchestrationAgentRoutingFacts(context.Context, db.ListOrchestrationAgentRoutingFactsParams) ([]db.ListOrchestrationAgentRoutingFactsRow, error) {
	i := f.listCalls
	f.listCalls++
	if i >= len(f.lists) {
		i = len(f.lists) - 1
	}
	if i < 0 {
		return nil, nil
	}
	return f.lists[i], nil
}
func (f *routingFactsFake) LockOrchestrationAgentRoutingFacts(context.Context, db.LockOrchestrationAgentRoutingFactsParams) (db.LockOrchestrationAgentRoutingFactsRow, error) {
	i := f.lockCalls
	f.lockCalls++
	if i >= len(f.locks) {
		i = len(f.locks) - 1
	}
	if i < 0 {
		return db.LockOrchestrationAgentRoutingFactsRow{}, pgx.ErrNoRows
	}
	return f.locks[i].row, f.locks[i].err
}

func TestSelectAndLockRoutingCandidateNoRowsIsRace(t *testing.T) {
	agentID := testRoutingUUID(1)
	row := routingTestRow(agentID, testRoutingUUID(2), 0)
	fake := &routingFactsFake{lists: [][]db.ListOrchestrationAgentRoutingFactsRow{{row}, {}}, locks: []routingFactsLockResult{{err: pgx.ErrNoRows}}}
	result, err := selectAndLockRoutingCandidate(context.Background(), fake, testRoutingUUID(9), testRoutingUUID(3), routingRepositoryTestSnapshot(), "", "")
	if err != nil || result.Selected != nil || fake.lockCalls != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, fake.lockCalls)
	}
}

func TestSelectAndLockRoutingCandidateUsesPostLockLoad(t *testing.T) {
	agentID := testRoutingUUID(1)
	runtimeID := testRoutingUUID(2)
	initial := routingTestRow(agentID, runtimeID, 0)
	postList := routingTestRow(agentID, runtimeID, 1)
	locked := db.LockOrchestrationAgentRoutingFactsRow{AgentID: agentID, RuntimeID: runtimeID, AgentCreatedAt: initial.AgentCreatedAt, AgentOwnerID: initial.AgentOwnerID, PermissionMode: "owner_only", Model: initial.Model, MaxConcurrentTasks: 1, RuntimeStatus: "online", RuntimeProvider: "p", RuntimeOwnerID: initial.RuntimeOwnerID, RuntimeMetadata: []byte(`{"capabilities":[]}`), HasWorkspaceInvocationTarget: true, HasMemberInvocationTarget: true, CurrentLoad: 1}
	fake := &routingFactsFake{lists: [][]db.ListOrchestrationAgentRoutingFactsRow{{initial}, {postList}}, locks: []routingFactsLockResult{{row: locked}}}
	result, err := selectAndLockRoutingCandidate(context.Background(), fake, testRoutingUUID(9), testRoutingUUID(3), routingRepositoryTestSnapshot(), "", "")
	if err != nil || result.Selected != nil {
		t.Fatalf("post-lock capacity should reject: result=%#v err=%v", result, err)
	}
}

func routingRepositoryTestSnapshot() RolePolicySnapshot {
	return RolePolicySnapshot{WorkspaceID: uuidText(testRoutingUUID(9)), Duty: DutyExecutor, Config: RoleProfileConfig{MaxConcurrency: 1}}
}
func routingTestRow(agent, runtime pgtype.UUID, load int64) db.ListOrchestrationAgentRoutingFactsRow {
	return db.ListOrchestrationAgentRoutingFactsRow{AgentID: agent, RuntimeID: runtime, AgentCreatedAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}, AgentOwnerID: testRoutingUUID(3), PermissionMode: "owner_only", Model: pgtype.Text{String: "m", Valid: true}, MaxConcurrentTasks: 1, RuntimeStatus: pgtype.Text{String: "online", Valid: true}, RuntimeProvider: pgtype.Text{String: "p", Valid: true}, RuntimeMetadata: []byte(`{"capabilities":[]}`), RuntimeOwnerID: testRoutingUUID(3), CurrentLoad: load, HasWorkspaceInvocationTarget: true, HasMemberInvocationTarget: true}
}
func testRoutingUUID(n byte) pgtype.UUID {
	value := uuid.Nil
	value[15] = n
	return pgtype.UUID{Bytes: value, Valid: true}
}

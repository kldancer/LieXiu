package orchestration

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestNormalizeStartRolePolicyBindingsRequiresExactFixedDuties(t *testing.T) {
	bindings := []RolePolicyBinding{
		{Duty: DutyReviewer, ProfileKey: "reviewer", Version: 2},
		{Duty: DutyIntegrator, ProfileKey: "integrator", Version: 3},
		{Duty: DutyExecutor, ProfileKey: "executor", Version: 1},
	}
	normalized, errs := normalizeStartRolePolicyBindings(bindings)
	if len(errs) > 0 {
		t.Fatalf("normalize bindings: %#v", errs)
	}
	got := []Duty{normalized[0].Duty, normalized[1].Duty, normalized[2].Duty}
	want := []Duty{DutyExecutor, DutyIntegrator, DutyReviewer}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted duties = %#v, want %#v", got, want)
	}
}

func TestNormalizeStartRolePolicyBindingsRejectsMissingDuplicateAndPlanner(t *testing.T) {
	_, errs := normalizeStartRolePolicyBindings([]RolePolicyBinding{
		{Duty: DutyExecutor, ProfileKey: "executor", Version: 1},
		{Duty: DutyExecutor, ProfileKey: "executor-alt", Version: 1},
		{Duty: DutyPlanner, ProfileKey: "planner", Version: 1},
	})
	codes := map[string]bool{}
	for _, item := range errs {
		codes[item.Code] = true
	}
	for _, code := range []string{"duplicate_duty", "invalid_duty", "missing_duty"} {
		if !codes[code] {
			t.Fatalf("errors %#v do not contain %q", errs, code)
		}
	}
}

func TestNormalizePlannerRolePolicyBindingRequiresPlanner(t *testing.T) {
	_, errs := normalizePlannerRolePolicyBinding(RolePolicyBinding{Duty: DutyExecutor, ProfileKey: "planner", Version: 1})
	if len(errs) != 1 || errs[0].Path != "role_policy_binding.duty" || errs[0].Code != "invalid_duty" {
		t.Fatalf("planner binding errors = %#v", errs)
	}
}

func TestRolePolicySnapshotContentIsCanonicalAndIncludesExplicitBinding(t *testing.T) {
	agentID := newTestUUID()
	profile := RoleProfileVersion{
		ID: "68a5d987-1870-4aa6-9c17-b0d7ee62ad73", ProfileKey: "go-executor", Version: 4,
		Duty: DutyExecutor, Name: "Go executor", Description: "Builds Go services",
		Config: RoleProfileConfig{
			RequiredCapabilities: []string{"go", "postgres"}, TimeoutSeconds: 900, MaxConcurrency: 2,
		},
	}
	binding := RolePolicyBinding{Duty: DutyExecutor, ProfileKey: profile.ProfileKey, Version: profile.Version, AgentID: agentID}
	first, firstHash, err := rolePolicySnapshotContent(profile, binding)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := rolePolicySnapshotContent(profile, binding)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstHash != secondHash || len(firstHash) != 64 {
		t.Fatalf("snapshot content is not stable: %q/%q", firstHash, secondHash)
	}
	if firstHash == "" || !rolePolicyBindingMatchesSnapshot(binding, RolePolicySnapshot{
		Duty: DutyExecutor, RoleProfileKey: profile.ProfileKey, RoleProfileVersion: profile.Version, AgentID: uuidText(agentID),
	}) {
		t.Fatal("explicit binding identity was not preserved")
	}
}

func TestMapRolePolicySnapshotKeepsNullableAgentBindingEmpty(t *testing.T) {
	workspaceID, missionID, profileID, snapshotID, frozenBy := newTestUUID(), newTestUUID(), newTestUUID(), newTestUUID(), newTestUUID()
	config := RoleProfileConfig{
		Instructions: "planner", RequiredCapabilities: []string{},
		Runtime:        RoleRuntimePreferences{AllowedRuntimeIDs: []string{}, PreferredRuntimeIDs: []string{}, Providers: []string{}, Models: []string{}},
		Tools:          RoleToolPermissions{AllowedTools: []string{}, AllowedPaths: []string{}},
		Budget:         RoleBudgetLimits{MaxReworkCycles: 1, MaxTechnicalRetries: 1},
		TimeoutSeconds: 900, MaxConcurrency: 1,
	}
	profile := RoleProfileVersion{
		ID: uuidText(profileID), ProfileKey: "planner", Version: 1, Duty: DutyPlanner,
		Name: "Planner", Config: config,
	}
	_, contentHash, err := rolePolicySnapshotContent(profile, RolePolicyBinding{Duty: DutyPlanner, ProfileKey: profile.ProfileKey, Version: profile.Version})
	if err != nil {
		t.Fatal(err)
	}
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := mapRolePolicySnapshot(db.MissionRolePolicySnapshot{
		ID: snapshotID, WorkspaceID: workspaceID, MissionID: missionID,
		Duty: DutyPlanner.String(), RoleProfileID: profileID, RoleProfileKey: profile.ProfileKey,
		RoleProfileVersion: profile.Version, ProfileName: profile.Name, Config: encodedConfig,
		AgentID: pgtype.UUID{}, SchemaVersion: RolePolicySnapshotSchemaVersion, ContentHash: contentHash,
		FrozenBy: frozenBy, FrozenAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AgentID != "" {
		t.Fatalf("nullable agent binding mapped to %q, want empty", snapshot.AgentID)
	}
	if !rolePolicyBindingMatchesSnapshot(RolePolicyBinding{Duty: DutyPlanner, ProfileKey: profile.ProfileKey, Version: profile.Version}, snapshot) {
		t.Fatal("nullable binding no longer matches its frozen snapshot")
	}
}

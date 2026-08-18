package orchestration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func seedRolePolicyBindings(
	t *testing.T,
	ctx context.Context,
	repository *Repository,
	workspaceID, actorID pgtype.UUID,
	duties ...Duty,
) []RolePolicyBinding {
	t.Helper()
	bindings := make([]RolePolicyBinding, 0, len(duties))
	for _, duty := range duties {
		key := "test-" + duty.String() + "-" + uuid.NewString()[:8]
		created, err := repository.CreateRoleProfileVersion(ctx, CreateRoleProfileVersionParams{
			WorkspaceID: workspaceID,
			CommandID:   newTestUUID(),
			ActorID:     actorID,
			ProfileKey:  key,
			Duty:        duty,
			Name:        "Test " + duty.String(),
			Config:      testRolePolicyConfig("v1"),
		})
		if err != nil {
			t.Fatalf("seed %s RoleProfile: %v", duty, err)
		}
		bindings = append(bindings, RolePolicyBinding{
			Duty: duty, ProfileKey: created.Profile.ProfileKey, Version: created.Profile.Version,
		})
	}
	return bindings
}

func createNextRolePolicyBinding(
	t *testing.T,
	ctx context.Context,
	repository *Repository,
	workspaceID, actorID pgtype.UUID,
	previous RolePolicyBinding,
) RolePolicyBinding {
	t.Helper()
	created, err := repository.CreateRoleProfileVersion(ctx, CreateRoleProfileVersionParams{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: actorID,
		ProfileKey: previous.ProfileKey, Duty: previous.Duty, Name: "Updated " + previous.Duty.String(),
		Config: testRolePolicyConfig("v2"),
	})
	if err != nil {
		t.Fatalf("create next %s RoleProfile version: %v", previous.Duty, err)
	}
	return RolePolicyBinding{Duty: previous.Duty, ProfileKey: previous.ProfileKey, Version: created.Profile.Version, AgentID: previous.AgentID}
}

func testRolePolicyConfig(instructions string) RoleProfileConfig {
	return RoleProfileConfig{
		Instructions:         instructions,
		RequiredCapabilities: []string{},
		Runtime: RoleRuntimePreferences{
			AllowedRuntimeIDs: []string{}, PreferredRuntimeIDs: []string{}, Providers: []string{}, Models: []string{},
		},
		Tools:          RoleToolPermissions{AllowedTools: []string{}, AllowedPaths: []string{}},
		Budget:         RoleBudgetLimits{MaxReworkCycles: 1, MaxTechnicalRetries: 1},
		TimeoutSeconds: 900, MaxConcurrency: 1,
	}
}

func rolePolicyBindingFor(t *testing.T, bindings []RolePolicyBinding, duty Duty) RolePolicyBinding {
	t.Helper()
	for _, binding := range bindings {
		if binding.Duty == duty {
			return binding
		}
	}
	t.Fatalf("missing test RolePolicy binding for duty %s", duty)
	return RolePolicyBinding{}
}

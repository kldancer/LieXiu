package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
)

func seedServiceRolePolicyBindings(
	t *testing.T,
	ctx context.Context,
	repository *orchestration.Repository,
	workspaceID, actorID pgtype.UUID,
	duties ...orchestration.Duty,
) []orchestration.RolePolicyBinding {
	t.Helper()
	bindings := make([]orchestration.RolePolicyBinding, 0, len(duties))
	for _, duty := range duties {
		key := "test-" + duty.String() + "-" + uuid.NewString()[:8]
		created, err := repository.CreateRoleProfileVersion(ctx, orchestration.CreateRoleProfileVersionParams{
			WorkspaceID: workspaceID, CommandID: serviceRolePolicyUUID(), ActorID: actorID,
			ProfileKey: key, Duty: duty, Name: "Test " + duty.String(),
			Config: orchestration.RoleProfileConfig{
				RequiredCapabilities: []string{},
				Runtime: orchestration.RoleRuntimePreferences{
					AllowedRuntimeIDs: []string{}, PreferredRuntimeIDs: []string{}, Providers: []string{}, Models: []string{},
				},
				Tools:          orchestration.RoleToolPermissions{AllowedTools: []string{}, AllowedPaths: []string{}},
				Budget:         orchestration.RoleBudgetLimits{MaxReworkCycles: 1, MaxTechnicalRetries: 1},
				TimeoutSeconds: 900, MaxConcurrency: 1,
			},
		})
		if err != nil {
			t.Fatalf("seed %s RoleProfile: %v", duty, err)
		}
		bindings = append(bindings, orchestration.RolePolicyBinding{Duty: duty, ProfileKey: created.Profile.ProfileKey, Version: created.Profile.Version})
	}
	return bindings
}

func serviceRolePolicyUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}

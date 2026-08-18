package orchestration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestRoleProfileVersionsAreImmutableIdempotentAndSerialized(t *testing.T) {
	ctx := context.Background()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; skipping RoleProfile integration test")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)
	var schemaReady bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('role_profile') IS NOT NULL`).Scan(&schemaReady); err != nil {
		t.Fatalf("check RoleProfile schema: %v", err)
	}
	if !schemaReady {
		t.Skip("RoleProfile migrations are not applied")
	}

	suffix := uuid.NewString()
	var ownerID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('RoleProfile owner', $1) RETURNING id`, "role-profile-"+suffix+"@liexiu.test").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ('RoleProfile test', $1, '', 'RPF') RETURNING id`, "role-profile-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM role_profile WHERE workspace_id=$1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM workspace WHERE id=$1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id=$1`, ownerID)
	})

	queries := db.New(pool)
	service := NewService(queries, NewRepository(queries, pool), nil, DefaultPlanHardLimits())
	base := CreateRoleProfileVersionCommand{
		WorkspaceID: workspaceID, CommandID: newTestUUID(), ActorID: ownerID,
		ProfileKey: "go-reviewer", Duty: DutyReviewer, Name: "Go reviewer",
		Config: RoleProfileConfig{
			Instructions: "Review Go changes", RequiredCapabilities: []string{"go"},
			TimeoutSeconds: 1800, MaxConcurrency: 1,
		},
	}
	created, err := service.CreateRoleProfileVersion(ctx, base)
	if err != nil {
		t.Fatalf("create RoleProfile v1: %v", err)
	}
	if created.Idempotent || created.Profile.Version != 1 || created.Profile.Duty != DutyReviewer {
		t.Fatalf("unexpected v1 result: %#v", created)
	}
	replayed, err := service.CreateRoleProfileVersion(ctx, base)
	if err != nil || !replayed.Idempotent || replayed.Profile.ID != created.Profile.ID {
		t.Fatalf("idempotent replay: result=%#v err=%v", replayed, err)
	}
	conflict := base
	conflict.Name = "Changed under reused command"
	if _, err := service.CreateRoleProfileVersion(ctx, conflict); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("reused command with changed payload: got %v, want ErrCommandConflict", err)
	}

	commands := []CreateRoleProfileVersionCommand{base, base}
	commands[0].CommandID = newTestUUID()
	commands[0].Name = "Go reviewer v2"
	commands[1].CommandID = newTestUUID()
	commands[1].Name = "Go reviewer v3"
	versions := make(chan int32, len(commands))
	errs := make(chan error, len(commands))
	var wg sync.WaitGroup
	for _, command := range commands {
		wg.Add(1)
		go func(command CreateRoleProfileVersionCommand) {
			defer wg.Done()
			result, createErr := service.CreateRoleProfileVersion(ctx, command)
			if createErr == nil {
				versions <- result.Profile.Version
			}
			errs <- createErr
		}(command)
	}
	wg.Wait()
	close(versions)
	close(errs)
	for createErr := range errs {
		if createErr != nil {
			t.Fatalf("concurrent version create: %v", createErr)
		}
	}
	seen := map[int32]bool{}
	for version := range versions {
		seen[version] = true
	}
	if !seen[2] || !seen[3] || len(seen) != 2 {
		t.Fatalf("serialized versions = %#v, want 2 and 3", seen)
	}
	latest, err := service.ListLatestRoleProfiles(ctx, workspaceID)
	if err != nil || len(latest) != 1 || latest[0].Version != 3 {
		t.Fatalf("latest RoleProfiles: %#v err=%v", latest, err)
	}
	var rows, distinctVersions int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT version) FROM role_profile WHERE workspace_id=$1 AND profile_key='go-reviewer'`, workspaceID).Scan(&rows, &distinctVersions); err != nil {
		t.Fatal(err)
	}
	if rows != 3 || distinctVersions != 3 {
		t.Fatalf("persisted versions rows=%d distinct=%d, want 3/3", rows, distinctVersions)
	}
}

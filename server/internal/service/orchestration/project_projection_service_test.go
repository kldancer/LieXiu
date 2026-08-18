package orchestration

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestGetProjectCommandCenterProjectionIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	fixture := newRoutingIntegrationFixture(t, ctx, pool)
	queries := db.New(pool)
	repository := NewRepository(queries, pool)
	service := NewService(queries, repository, nil, DefaultPlanHardLimits())
	projectID := insertProjectForProjectionTest(t, ctx, pool, fixture.workspaceID)

	t.Run("empty project is scoped and not truncated", func(t *testing.T) {
		projection, err := service.GetProjectCommandCenterProjection(ctx, fixture.workspaceID, projectID)
		if err != nil {
			t.Fatal(err)
		}
		if projection.Project.ID != uuidText(projectID) || len(projection.Missions) != 0 || projection.Truncated {
			t.Fatalf("empty projection=%#v", projection)
		}
		wrongWorkspace := newTestUUID()
		if _, err := service.GetProjectCommandCenterProjection(ctx, wrongWorkspace, projectID); err == nil {
			t.Fatal("cross-workspace project read unexpectedly succeeded")
		}
	})

	t.Run("101 missions are bounded at 100 and marked truncated", func(t *testing.T) {
		for i := 0; i < MaxProjectCommandCenterMissions+1; i++ {
			insertMissionRootForProjectionTest(t, ctx, pool, fixture.workspaceID, projectID, fixture.ownerID, i)
		}
		projection, err := service.GetProjectCommandCenterProjection(ctx, fixture.workspaceID, projectID)
		if err != nil {
			t.Fatal(err)
		}
		if len(projection.Missions) != MaxProjectCommandCenterMissions || !projection.Truncated {
			t.Fatalf("missions=%d truncated=%v", len(projection.Missions), projection.Truncated)
		}
	})

	t.Run("cancelled child read fails closed", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		projection, err := service.GetProjectCommandCenterProjection(cancelled, fixture.workspaceID, projectID)
		if err == nil || projection.Project.ID != "" || projection.Missions != nil {
			t.Fatalf("child failure was not fail-closed: projection=%#v err=%v", projection, err)
		}
	})
}

func insertProjectForProjectionTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID pgtype.UUID) pgtype.UUID {
	t.Helper()
	var projectID pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO project (workspace_id, title, status, priority) VALUES ($1, 'Projection test project', 'in_progress', 'medium') RETURNING id`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM issue WHERE project_id=$1`, projectID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM project WHERE id=$1`, projectID)
	})
	return projectID
}

func insertMissionRootForProjectionTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID, ownerID pgtype.UUID, index int) {
	t.Helper()
	var issueID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, project_id)
		VALUES ($1, $2, 'todo', 'none', 'member', $3, $4, $5) RETURNING id
	`, workspaceID, "Projection mission "+uuid.NewString(), ownerID, 10000+index, projectID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mission (issue_id, workspace_id, status, created_by) VALUES ($1, $2, 'draft', $3)`, issueID, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}
}

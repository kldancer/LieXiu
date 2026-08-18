package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
)

func TestGetProjectCommandCenterHTTPMapping(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler database fixture unavailable")
	}
	ctx := context.Background()
	projectID := insertHandlerCommandCenterProject(t, ctx)
	queries := testHandler.Queries
	repository := orchestration.NewRepository(queries, testPool)
	service := orchestration.NewService(queries, repository, nil, orchestration.DefaultPlanHardLimits())
	previous := testHandler.Orchestration
	t.Cleanup(func() { testHandler.Orchestration = previous })

	t.Run("400 invalid id", func(t *testing.T) {
		testHandler.Orchestration = service
		record := httptest.NewRecorder()
		testHandler.GetProjectCommandCenter(record, projectCommandCenterRequest("not-a-uuid"))
		if record.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", record.Code, record.Body)
		}
	})
	t.Run("503 missing orchestration service", func(t *testing.T) {
		testHandler.Orchestration = nil
		record := httptest.NewRecorder()
		testHandler.GetProjectCommandCenter(record, projectCommandCenterRequest(projectID))
		if record.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", record.Code, record.Body)
		}
	})
	t.Run("404 project is not in workspace", func(t *testing.T) {
		testHandler.Orchestration = service
		record := httptest.NewRecorder()
		testHandler.GetProjectCommandCenter(record, projectCommandCenterRequest(newTestUUIDString()))
		if record.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", record.Code, record.Body)
		}
	})
	t.Run("500 orchestration failure is not exposed", func(t *testing.T) {
		testHandler.Orchestration = &orchestration.Service{}
		record := httptest.NewRecorder()
		testHandler.GetProjectCommandCenter(record, projectCommandCenterRequest(projectID))
		if record.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", record.Code, record.Body)
		}
		if record.Body.String() == "" || containsHandlerSecret(record.Body.String()) {
			t.Fatalf("unsafe error body=%s", record.Body)
		}
	})
	t.Run("200 empty project returns bounded envelope", func(t *testing.T) {
		testHandler.Orchestration = service
		record := httptest.NewRecorder()
		testHandler.GetProjectCommandCenter(record, projectCommandCenterRequest(projectID))
		if record.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", record.Code, record.Body)
		}
		var body struct {
			Missions  []json.RawMessage `json:"missions"`
			Truncated bool              `json:"truncated"`
		}
		if err := json.Unmarshal(record.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Missions == nil || len(body.Missions) != 0 || body.Truncated {
			t.Fatalf("body=%s", record.Body)
		}
	})
}

func insertHandlerCommandCenterProject(t *testing.T, ctx context.Context) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(ctx, `INSERT INTO project (workspace_id, title, status, priority) VALUES ($1, 'Handler projection project', 'in_progress', 'medium') RETURNING id`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id=$1`, projectID) })
	return projectID
}

func projectCommandCenterRequest(projectID string) *http.Request {
	req := newRequest(http.MethodGet, "/api/projects/"+projectID+"/command-center", nil)
	return withURLParam(req, "id", projectID)
}

func newTestUUIDString() string               { return "00000000-0000-0000-0000-000000000099" }
func containsHandlerSecret(value string) bool { return value == "secret" || len(value) > 4096 }

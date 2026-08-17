package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/kailonyang/liexiu/server/internal/middleware"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// withChatTestWorkspaceCtx injects the workspace and member context used by
// direct handler tests that exercise task and attachment behavior.
func withChatTestWorkspaceCtx(t *testing.T, req *http.Request) *http.Request {
	t.Helper()
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load test member row: %v", err)
	}
	return req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, memberRow))
}

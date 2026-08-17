package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateComment_StripsNullBytesInsteadOf500 pins the fix for GH #5388.
//
// A comment whose content carries a byte PostgreSQL's TEXT type cannot store —
// most commonly an embedded NUL (SQLSTATE 22021) that survives a JSON round
// trip from `--content-file` — must post successfully with the offending byte
// stripped, not fail the INSERT with an opaque 500 the CLI renders as a
// generic "server unavailable" (and then retries forever).
func TestCreateComment_StripsNullBytesInsteadOf500(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	issueID := createTestIssue(t, "null-byte comment fixture (GH #5388)", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	w := httptest.NewRecorder()
	r := newRequest("POST", "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "diagnosis body\x00 with a stray NUL byte",
	})
	r = withURLParam(r, "id", issueID)

	testHandler.CreateComment(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateComment with NUL byte: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got, _ := body["content"].(string)
	if strings.ContainsRune(got, '\x00') {
		t.Fatalf("stored content still contains a NUL byte: %q", got)
	}
	if want := "diagnosis body with a stray NUL byte"; got != want {
		t.Fatalf("stored content: expected %q (NUL stripped), got %q", want, got)
	}
}

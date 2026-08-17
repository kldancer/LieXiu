package handler

import (
	"fmt"
	"testing"
)

// TestCreateComment_BlockedMentionReasonDoesNotEnumeratePrivateAgent pins the
// enumeration-safety rule (MUL-4525 §2): a mention the author cannot invoke and a
// mention of a truly nonexistent agent both return the same generic
// invocation_not_allowed, so a blocked reason can never confirm a private
// agent's existence.
func TestCreateComment_BlockedMentionReasonDoesNotEnumeratePrivateAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	privateAgentID, _, _ := privateAgentTestFixture(t)
	issueID := createCommentTriggerPreviewIssue(t, "blocked mention enumeration safety", "", "")
	nonexistentID := "00000000-0000-0000-0000-0000000000ff"

	content := fmt.Sprintf(
		"[@Private](mention://agent/%s) [@Ghost](mention://agent/%s) ping",
		privateAgentID, nonexistentID,
	)
	preview := previewCommentTriggersForTest(t, issueID, map[string]any{"content": content})
	if len(preview.Agents) != 0 {
		t.Fatalf("preview agents = %+v, want none", preview.Agents)
	}
	if len(preview.Blocked) != 2 {
		t.Fatalf("preview blocked = %+v, want 2", preview.Blocked)
	}
	for _, b := range preview.Blocked {
		if b.ReasonCode != ReasonInvocationNotAllowed {
			t.Errorf("blocked %s reason = %q, want invocation_not_allowed (must not distinguish private-exists from not-found)", b.TargetID, b.ReasonCode)
		}
	}
}

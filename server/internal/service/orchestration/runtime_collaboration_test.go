package orchestration

import (
	"testing"

	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func TestMailboxMessageTypeForRuntimeOperationFreezesSevenOperations(t *testing.T) {
	tests := map[protocol.RuntimeCollaborationOperation]MailboxMessageType{
		protocol.RuntimeCollaborationRequestContext:     MailboxMessageContextRequest,
		protocol.RuntimeCollaborationRespondContext:     MailboxMessageContextResponse,
		protocol.RuntimeCollaborationSendHandoff:        MailboxMessageHandoff,
		protocol.RuntimeCollaborationNotifyArtifact:     MailboxMessageArtifactNotice,
		protocol.RuntimeCollaborationSendReviewFeedback: MailboxMessageReviewFeedback,
		protocol.RuntimeCollaborationReportBlocker:      MailboxMessageBlocker,
		protocol.RuntimeCollaborationRequestDecision:    MailboxMessageDecisionRequest,
	}
	for operation, want := range tests {
		if !operation.Valid() {
			t.Fatalf("operation %q is not valid", operation)
		}
		got, err := MailboxMessageTypeForRuntimeOperation(operation)
		if err != nil || got != want {
			t.Fatalf("operation %q mapped to %q, %v; want %q", operation, got, err, want)
		}
	}
	if _, err := MailboxMessageTypeForRuntimeOperation("chat"); err == nil {
		t.Fatal("provider-specific/unbounded operation unexpectedly accepted")
	}
}

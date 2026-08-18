package orchestration

import (
	"fmt"
	"time"

	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

const RuntimeCollaborationDefaultTTL = 24 * time.Hour

// MailboxMessageTypeForRuntimeOperation is the single mapping shared by every
// Runtime adapter. Providers may choose a different presentation layer, but
// cannot invent provider-specific mailbox semantics.
func MailboxMessageTypeForRuntimeOperation(operation protocol.RuntimeCollaborationOperation) (MailboxMessageType, error) {
	switch operation {
	case protocol.RuntimeCollaborationRequestContext:
		return MailboxMessageContextRequest, nil
	case protocol.RuntimeCollaborationRespondContext:
		return MailboxMessageContextResponse, nil
	case protocol.RuntimeCollaborationSendHandoff:
		return MailboxMessageHandoff, nil
	case protocol.RuntimeCollaborationNotifyArtifact:
		return MailboxMessageArtifactNotice, nil
	case protocol.RuntimeCollaborationSendReviewFeedback:
		return MailboxMessageReviewFeedback, nil
	case protocol.RuntimeCollaborationReportBlocker:
		return MailboxMessageBlocker, nil
	case protocol.RuntimeCollaborationRequestDecision:
		return MailboxMessageDecisionRequest, nil
	default:
		return "", fmt.Errorf("unsupported runtime collaboration operation %q", operation)
	}
}

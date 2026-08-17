package main

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

// internalOnlyPayloadKeys lists payload keys that exist purely for in-process
// listeners and must never be serialized to a WebSocket client.
//
// `issue:updated` carries prev_description and prev_title so the in-process
// listeners can diff against the new values; activity_listeners.go records the
// title change. Those listeners run on
// bus.Subscribe, which Publish dispatches BEFORE the SubscribeAll forwarder
// below, so removing the keys on the way out cannot affect them.
//
// No client reads either key — IssueUpdatedPayload in
// packages/core/types/events.ts does not declare them. They reached the wire
// only because the forwarder reuses the producer's payload map verbatim, which
// meant every description autosave broadcast TWO full copies of the description
// (the new one inside `issue`, plus prev_description) to every connection in the
// workspace, including users who did not have the issue open. The DB write is
// O(1); the fanout was O(workspace connections × description size) (MUL-5492).
//
// This is a table rather than an `if` on one event type because the bug was
// structural, not a typo: the next large field added to a published payload
// inherits the same cost silently. Keeping the list declarative puts the
// internal/external payload boundary in one reviewable place.
var internalOnlyPayloadKeys = map[string][]string{
	protocol.EventIssueUpdated: {"prev_description", "prev_title"},
	// task:failed error text may contain provider/runtime detail that does not
	// belong in the workspace-wide realtime fanout.
	protocol.EventTaskFailed: {"error"},
}

// projectOutbound returns payload with the event type's internal-only keys
// removed, ready to serialize for external consumers.
//
// The input map is never mutated. In-process listeners have already run by the
// time this is called, but the producer still owns the map and a second
// forwarder may yet read it, so mutating it in place would be a landmine.
func projectOutbound(eventType string, payload any) any {
	keys := internalOnlyPayloadKeys[eventType]
	if len(keys) == 0 {
		return payload
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	projected := make(map[string]any, len(m))
	for k, v := range m {
		projected[k] = v
	}
	for _, k := range keys {
		delete(projected, k)
	}
	return projected
}

// registerListeners wires up event bus listeners for WS broadcasting.
//
// The broadcaster parameter is intentionally typed as the realtime.Broadcaster
// interface (not *realtime.Hub) so that this layer can later be swapped out
// for a Redis-backed relay or a feature-flagged dual-write implementation
// without touching any of the event listeners below. This is Phase 0 of the
// horizontal-scaling plan tracked in MUL-1138.
func registerListeners(bus *events.Bus, b realtime.Broadcaster) {
	// SubscribeAll handles workspace-broadcast events.
	bus.SubscribeAll(func(e events.Event) {
		msg := map[string]any{
			"type":       e.Type,
			"payload":    projectOutbound(e.Type, e.Payload),
			"actor_id":   e.ActorID,
			"actor_type": e.ActorType,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Error("failed to marshal event", "event_type", e.Type, "error", err)
			return
		}

		// Phase 1 (MUL-1138): the per-resource scope routing for high-frequency
		// task events is intentionally NOT enabled yet. The server-side
		// pieces — Hub.subscribe/unsubscribe protocol, ScopeAuthorizer, Redis
		// Streams relay — have all landed, but the client (WSClient + the
		// per-page task hooks) does not yet send `subscribe` frames or
		// replay subscriptions on reconnect. Routing these events through
		// `BroadcastToScope("task", ...)` today would silently drop task
		// messages on the floor, breaking the pending-task UI.
		//
		// Until the client lands its scope-subscription PR, we keep
		// task events on workspace fanout (same behavior as before this PR).
		// The `Event.TaskID` hint remains populated so flipping the switch later
		// is a one-line change here. See review on PR #1429 for context.

		if e.WorkspaceID != "" {
			realtime.M.RecordEvent(e.Type)
			b.BroadcastToWorkspace(e.WorkspaceID, data)
		} else if strings.HasPrefix(e.Type, "daemon:") {
			realtime.M.RecordEvent(e.Type)
			b.Broadcast(data)
		}
		// Otherwise drop — no global broadcast for non-daemon events without a workspace.
	})
}

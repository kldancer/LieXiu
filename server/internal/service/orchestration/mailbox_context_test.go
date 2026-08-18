package orchestration

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestBuildMailboxRunContextBoundsAndHashes(t *testing.T) {
	recipient := contextTestUUID()
	now := time.Date(2026, 8, 18, 8, 0, 0, 0, time.UTC)
	rows := make([]db.OrchestrationMailboxMessage, 0, 17)
	for index := 0; index < 17; index++ {
		payload, _ := json.Marshal(map[string]string{"body": string(bytes.Repeat([]byte{'x'}, 10*1024)), "ordinal": string(rune('a' + index))})
		rows = append(rows, db.OrchestrationMailboxMessage{
			ID: contextTestUUID(), WorkspaceID: contextTestUUID(), MissionID: contextTestUUID(),
			SchemaVersion: 1, Type: string(MailboxMessageHandoff),
			SenderType: string(MailboxActorAgent), SenderID: contextTestUUID(),
			RecipientType: string(MailboxActorAgent), RecipientID: recipient,
			Status: string(MailboxStatusPending), DedupeKey: "context-bound-" + string(rune('a'+index)),
			Hops: 0, PayloadVersion: 1, Payload: payload,
			CommandID: contextTestUUID(), CreatedBy: contextTestUUID(), Revision: 1,
			CreatedAt:       pgtype.Timestamptz{Time: now.Add(time.Duration(index) * time.Second), Valid: true},
			ExpiresAt:       pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
			StatusChangedAt: pgtype.Timestamptz{Time: now, Valid: true},
		})
	}
	contextSnapshot, selected, err := buildMailboxRunContext(recipient, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 6 || len(contextSnapshot.Messages) != 6 {
		t.Fatalf("selected rows/messages=%d/%d, want payload-bounded prefix of 6", len(selected), len(contextSnapshot.Messages))
	}
	for _, message := range contextSnapshot.Messages {
		want := message.ContentHash
		message.ContentHash = ""
		got, err := hashMailboxJSON(message)
		if err != nil || got != want {
			t.Fatalf("message hash=%s want=%s err=%v", got, want, err)
		}
	}
	wantContextHash := contextSnapshot.ContentHash
	contextSnapshot.ContentHash = ""
	gotContextHash, err := hashMailboxJSON(contextSnapshot)
	if err != nil || gotContextHash != wantContextHash {
		t.Fatalf("context hash=%s want=%s err=%v", gotContextHash, wantContextHash, err)
	}
	contextSnapshot.ContentHash = wantContextHash
	input, err := attachMailboxRunContext([]byte(`{"base":true}`), contextSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		Base           bool                `json:"base"`
		MailboxContext MailboxRunContextV1 `json:"mailbox_context"`
	}
	if err := json.Unmarshal(input, &frozen); err != nil || !frozen.Base || frozen.MailboxContext.ContentHash != wantContextHash {
		t.Fatalf("frozen input=%s context=%#v err=%v", input, frozen.MailboxContext, err)
	}
	if _, err := attachMailboxRunContext(input, contextSnapshot); err == nil {
		t.Fatal("duplicate mailbox_context attachment was accepted")
	}
}

func contextTestUUID() pgtype.UUID {
	value := uuid.New()
	return pgtype.UUID{Bytes: value, Valid: true}
}

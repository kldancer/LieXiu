package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
)

func TestWriteMissionCommandErrorProjectsRoutingEvidence(t *testing.T) {
	routing := orchestration.RoutingSelectionResult{
		SelectionVersion: orchestration.RoutingSelectionVersion,
		SnapshotHash:     "snapshot-hash",
		EvidenceHash:     "evidence-hash",
		Evaluations: []orchestration.RoutingCandidateEvaluation{{
			CandidateRef: "candidate_0123456789abcdef0123456789abcdef",
			ReasonCodes:  []string{orchestration.RoutingReasonCapabilityMissing},
		}},
	}
	recorder := httptest.NewRecorder()
	writeMissionCommandError(recorder, &orchestration.RoutingUnavailableError{Duty: orchestration.DutyPlanner, Routing: routing})

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	var payload struct {
		Error   string                               `json:"error"`
		Duty    orchestration.Duty                   `json:"duty"`
		Routing orchestration.RoutingSelectionResult `json:"routing"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error == "" || payload.Duty != orchestration.DutyPlanner || payload.Routing.SnapshotHash != routing.SnapshotHash || payload.Routing.EvidenceHash != routing.EvidenceHash {
		t.Fatalf("unexpected routing error payload: %#v", payload)
	}
}

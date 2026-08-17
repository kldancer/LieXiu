package orchestration

import "testing"

func TestArtifactAndReviewCommandValidation(t *testing.T) {
	identity := newTestUUID()
	artifact := RecordArtifactCommand{
		WorkspaceID: identity, MissionID: newTestUUID(), TaskNodeID: newTestUUID(),
		RunID: newTestUUID(), CommandID: newTestUUID(), ActorID: newTestUUID(),
		Kind: ArtifactKindCommit, URI: "repo://commit/abc", Metadata: []byte(`{}`),
	}
	if err := validateArtifactCommand(artifact); err != nil {
		t.Fatalf("valid artifact command: %v", err)
	}
	artifact.Metadata = []byte(`[]`)
	if err := validateArtifactCommand(artifact); err == nil {
		t.Fatal("array artifact metadata was accepted")
	}

	review := RecordReviewVerdictCommand{
		WorkspaceID: identity, MissionID: newTestUUID(), TaskNodeID: newTestUUID(),
		ReviewRunID: newTestUUID(), ArtifactID: newTestUUID(), CommandID: newTestUUID(),
		ActorID: newTestUUID(), Decision: ReviewDecisionChangesRequested,
		Evidence: []byte(`{"tests":"passed"}`), RequestedChanges: []string{"add regression test"},
	}
	if err := validateReviewCommand(review); err != nil {
		t.Fatalf("valid review command: %v", err)
	}
	review.RequestedChanges = nil
	if err := validateReviewCommand(review); err == nil {
		t.Fatal("changes_requested without requested_changes was accepted")
	}
	review.Decision = ReviewDecision("maybe")
	if err := validateReviewCommand(review); err == nil {
		t.Fatal("unknown review decision was accepted")
	}
}

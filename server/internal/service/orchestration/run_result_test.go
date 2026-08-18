package orchestration

import (
	"testing"

	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

func TestTaskOutputAllowsAdditionalDaemonEnvelopeFields(t *testing.T) {
	output, err := taskOutput([]byte(`{"output":"{}","session":"s","usage":{"output_tokens":2},"workdir":"/tmp"}`))
	if err != nil {
		t.Fatalf("taskOutput() error = %v", err)
	}
	if string(output) != "{}" {
		t.Fatalf("taskOutput() = %q, want {}", output)
	}
}

func TestExecutionReceiptRejectsUnknownFieldsAndMultipleValues(t *testing.T) {
	valid := `{"schema_version":1,"artifact":{"kind":"file","uri":"agent-task://x","content_hash":"sha256:x","summary":"ok","metadata":{}}}`
	if _, err := decodeExecutionArtifactReceipt([]byte(valid + ` {}`)); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
	if _, err := decodeExecutionArtifactReceipt([]byte(`{"schema_version":1,"extra":true,"artifact":{"kind":"file","uri":"x","content_hash":"","summary":"","metadata":{}}}`)); err == nil {
		t.Fatal("unknown receipt field was accepted")
	}
}

func TestArtifactReceiptKindMustBeAllowedBeforeReconcileMutation(t *testing.T) {
	node := db.TaskNode{ArtifactKinds: []byte(`["commit"]`)}
	allowed, err := artifactKindAllowedByNode(node, ArtifactKindFile)
	if err != nil {
		t.Fatalf("artifactKindAllowedByNode() error = %v", err)
	}
	if allowed {
		t.Fatal("disallowed artifact kind was accepted")
	}
	allowed, err = artifactKindAllowedByNode(node, ArtifactKindCommit)
	if err != nil || !allowed {
		t.Fatalf("allowed artifact kind = %v, error = %v", allowed, err)
	}
}

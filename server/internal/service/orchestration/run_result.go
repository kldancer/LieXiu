package orchestration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	maxRunOutputBytes = 64 * 1024
	maxReceiptText    = 4096
	maxReceiptItems   = 128
)

type executionArtifactReceipt struct {
	SchemaVersion int `json:"schema_version"`
	Artifact      struct {
		Kind        ArtifactKind   `json:"kind"`
		URI         string         `json:"uri"`
		ContentHash string         `json:"content_hash"`
		Summary     string         `json:"summary"`
		Metadata    map[string]any `json:"metadata"`
	} `json:"artifact"`
}

type executionReviewReceipt struct {
	SchemaVersion    int            `json:"schema_version"`
	Decision         ReviewDecision `json:"decision"`
	Evidence         map[string]any `json:"evidence"`
	RequestedChanges []string       `json:"requested_changes"`
}

func decodeExecutionArtifactReceipt(raw []byte) (executionArtifactReceipt, error) {
	if len(raw) == 0 || len(raw) > maxRunOutputBytes {
		return executionArtifactReceipt{}, fmt.Errorf("artifact receipt output exceeds %d bytes", maxRunOutputBytes)
	}
	var receipt executionArtifactReceipt
	if err := decodeSingleJSON(raw, &receipt); err != nil {
		return receipt, fmt.Errorf("invalid artifact receipt: %w", err)
	}
	if receipt.SchemaVersion != 1 {
		return receipt, fmt.Errorf("artifact receipt schema_version must be 1")
	}
	if !receipt.Artifact.Kind.Valid() || receipt.Artifact.Kind == ArtifactKindPlanProposal {
		return receipt, fmt.Errorf("artifact receipt kind %q is not allowed", receipt.Artifact.Kind)
	}
	if strings.TrimSpace(receipt.Artifact.URI) == "" || len(receipt.Artifact.URI) > maxReceiptText {
		return receipt, fmt.Errorf("artifact receipt uri must contain 1 to %d bytes", maxReceiptText)
	}
	if len(receipt.Artifact.ContentHash) > maxReceiptText || len(receipt.Artifact.Summary) > maxReceiptText {
		return receipt, fmt.Errorf("artifact receipt text is too large")
	}
	if receipt.Artifact.Metadata == nil {
		receipt.Artifact.Metadata = map[string]any{}
	}
	if len(receipt.Artifact.Metadata) > maxReceiptItems {
		return receipt, fmt.Errorf("artifact receipt metadata has too many fields")
	}
	return receipt, nil
}

func decodeExecutionReviewReceipt(raw []byte) (executionReviewReceipt, error) {
	if len(raw) == 0 || len(raw) > maxRunOutputBytes {
		return executionReviewReceipt{}, fmt.Errorf("review receipt output exceeds %d bytes", maxRunOutputBytes)
	}
	var receipt executionReviewReceipt
	if err := decodeSingleJSON(raw, &receipt); err != nil {
		return receipt, fmt.Errorf("invalid review receipt: %w", err)
	}
	if receipt.SchemaVersion != 1 {
		return receipt, fmt.Errorf("review receipt schema_version must be 1")
	}
	switch receipt.Decision {
	case ReviewDecisionApproved, ReviewDecisionChangesRequested, ReviewDecisionRejected:
	default:
		return receipt, fmt.Errorf("review receipt decision %q is not supported", receipt.Decision)
	}
	if receipt.Evidence == nil {
		receipt.Evidence = map[string]any{}
	}
	if receipt.RequestedChanges == nil {
		receipt.RequestedChanges = []string{}
	}
	if len(receipt.Evidence) > maxReceiptItems {
		return receipt, fmt.Errorf("review receipt evidence has too many fields")
	}
	if len(receipt.RequestedChanges) > maxReceiptItems {
		return receipt, fmt.Errorf("review receipt requested_changes has too many items")
	}
	for _, change := range receipt.RequestedChanges {
		if strings.TrimSpace(change) == "" || len(change) > maxReceiptText {
			return receipt, fmt.Errorf("review receipt requested_changes contains invalid text")
		}
	}
	if receipt.Decision == ReviewDecisionChangesRequested && len(receipt.RequestedChanges) == 0 {
		return receipt, fmt.Errorf("review receipt requested_changes are required")
	}
	return receipt, nil
}

func decodeSingleJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func taskOutput(result []byte) ([]byte, error) {
	// The daemon result is a shared envelope. Newer daemon versions may add
	// operational fields, so strict field checking belongs to the receipt below
	// rather than to this outer object.
	var envelope struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Output) == 0 || string(envelope.Output) == "null" {
		return nil, fmt.Errorf("task result output is missing")
	}
	var output string
	if err := json.Unmarshal(envelope.Output, &output); err != nil {
		return nil, fmt.Errorf("task result output must be a string")
	}
	return []byte(output), nil
}

func (k ArtifactKind) Valid() bool {
	switch k {
	case ArtifactKindBranch, ArtifactKindCommit, ArtifactKindDiff, ArtifactKindFile, ArtifactKindTestReceipt, ArtifactKindFinalDelivery:
		return true
	default:
		return false
	}
}

package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const RolePolicySnapshotSchemaVersion = 1

var ErrRolePolicyAlreadyFrozen = errors.New("mission role policy is already frozen with a different binding")

// RolePolicyBinding is the owner's explicit selection of one immutable
// RoleProfile version for a Mission duty. AgentID is an optional Mission-level
// binding; candidate eligibility remains owned by deterministic routing.
type RolePolicyBinding struct {
	Duty       Duty
	ProfileKey string
	Version    int32
	AgentID    pgtype.UUID
}

// RolePolicySnapshot is the immutable, Mission-owned copy of the policy that
// later routing must consume. It intentionally copies the normalized config so
// a new global RoleProfile version cannot alter an existing Mission.
type RolePolicySnapshot struct {
	ID                 string            `json:"id"`
	WorkspaceID        string            `json:"workspace_id"`
	MissionID          string            `json:"mission_id"`
	SchemaVersion      int32             `json:"schema_version"`
	Duty               Duty              `json:"duty"`
	RoleProfileID      string            `json:"role_profile_id"`
	RoleProfileKey     string            `json:"role_profile_key"`
	RoleProfileVersion int32             `json:"role_profile_version"`
	ProfileName        string            `json:"profile_name"`
	ProfileDescription string            `json:"profile_description"`
	Config             RoleProfileConfig `json:"config"`
	AgentID            string            `json:"agent_id,omitempty"`
	ContentHash        string            `json:"content_hash"`
	FrozenBy           string            `json:"frozen_by"`
	FrozenAt           time.Time         `json:"frozen_at"`
}

type rolePolicySnapshotPayload struct {
	SchemaVersion      int32             `json:"schema_version"`
	Duty               Duty              `json:"duty"`
	RoleProfileID      string            `json:"role_profile_id"`
	RoleProfileKey     string            `json:"role_profile_key"`
	RoleProfileVersion int32             `json:"role_profile_version"`
	ProfileName        string            `json:"profile_name"`
	ProfileDescription string            `json:"profile_description"`
	Config             RoleProfileConfig `json:"config"`
	AgentID            string            `json:"agent_id,omitempty"`
}

func normalizePlannerRolePolicyBinding(binding RolePolicyBinding) (RolePolicyBinding, []ValidationError) {
	normalized, errs := normalizeRolePolicyBinding(binding, "role_policy_binding")
	if normalized.Duty != DutyPlanner {
		errs = append(errs, ValidationError{Path: "role_policy_binding.duty", Code: "invalid_duty", Message: "role_policy_binding duty must be planner"})
	}
	return normalized, errs
}

func normalizeStartRolePolicyBindings(bindings []RolePolicyBinding) ([]RolePolicyBinding, []ValidationError) {
	expected := map[Duty]bool{DutyExecutor: false, DutyReviewer: false, DutyIntegrator: false}
	normalized := make([]RolePolicyBinding, 0, len(bindings))
	var errs []ValidationError
	for index, binding := range bindings {
		path := fmt.Sprintf("role_policy_bindings[%d]", index)
		item, itemErrs := normalizeRolePolicyBinding(binding, path)
		errs = append(errs, itemErrs...)
		if _, ok := expected[item.Duty]; !ok {
			errs = append(errs, ValidationError{Path: path + ".duty", Code: "invalid_duty", Message: "start bindings only accept executor, reviewer, and integrator"})
		} else if expected[item.Duty] {
			errs = append(errs, ValidationError{Path: path + ".duty", Code: "duplicate_duty", Message: "each start duty must appear exactly once"})
		} else {
			expected[item.Duty] = true
		}
		normalized = append(normalized, item)
	}
	for _, duty := range []Duty{DutyExecutor, DutyReviewer, DutyIntegrator} {
		if !expected[duty] {
			errs = append(errs, ValidationError{Path: "role_policy_bindings", Code: "missing_duty", Message: fmt.Sprintf("role_policy_bindings must include %s", duty)})
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Duty < normalized[j].Duty })
	return normalized, errs
}

func normalizeRolePolicyBinding(binding RolePolicyBinding, path string) (RolePolicyBinding, []ValidationError) {
	binding.ProfileKey = strings.TrimSpace(binding.ProfileKey)
	var errs []ValidationError
	if !binding.Duty.Valid() {
		errs = append(errs, ValidationError{Path: path + ".duty", Code: "invalid_duty", Message: "duty must be planner, executor, reviewer, or integrator"})
	}
	if !roleProfileKeyPattern.MatchString(binding.ProfileKey) {
		errs = append(errs, ValidationError{Path: path + ".profile_key", Code: "invalid_profile_key", Message: "profile_key must identify an explicit RoleProfile"})
	}
	if binding.Version < 1 {
		errs = append(errs, ValidationError{Path: path + ".version", Code: "invalid_version", Message: "version must be at least 1"})
	}
	if binding.AgentID.Valid && !validUUID(binding.AgentID) {
		errs = append(errs, ValidationError{Path: path + ".agent_id", Code: "invalid_uuid", Message: "agent_id must be a non-zero UUID when provided"})
	}
	return binding, errs
}

func rolePolicySnapshotContent(profile RoleProfileVersion, binding RolePolicyBinding) ([]byte, string, error) {
	payload := rolePolicySnapshotPayload{
		SchemaVersion: RolePolicySnapshotSchemaVersion,
		Duty:          binding.Duty, RoleProfileID: profile.ID, RoleProfileKey: profile.ProfileKey,
		RoleProfileVersion: profile.Version, ProfileName: profile.Name,
		ProfileDescription: profile.Description, Config: profile.Config,
		AgentID: uuidText(binding.AgentID),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("encode role policy snapshot: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func rolePolicyBindingMatchesSnapshot(binding RolePolicyBinding, snapshot RolePolicySnapshot) bool {
	return binding.Duty == snapshot.Duty &&
		binding.ProfileKey == snapshot.RoleProfileKey &&
		binding.Version == snapshot.RoleProfileVersion &&
		optionalUUIDText(binding.AgentID) == snapshot.AgentID
}

func optionalUUIDText(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuidText(value)
}

func freezeRolePolicyBindings(
	ctx context.Context,
	qtx *db.Queries,
	workspaceID, missionID, actorID pgtype.UUID,
	bindings []RolePolicyBinding,
) ([]RolePolicySnapshot, error) {
	snapshots := make([]RolePolicySnapshot, 0, len(bindings))
	for _, binding := range bindings {
		existing, err := qtx.GetMissionRolePolicySnapshot(ctx, db.GetMissionRolePolicySnapshotParams{
			WorkspaceID: workspaceID, MissionID: missionID, Duty: binding.Duty.String(),
		})
		if err == nil {
			snapshot, mapErr := mapRolePolicySnapshot(existing)
			if mapErr != nil {
				return nil, mapErr
			}
			if !rolePolicyBindingMatchesSnapshot(binding, snapshot) {
				return nil, ErrRolePolicyAlreadyFrozen
			}
			snapshots = append(snapshots, snapshot)
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("freeze role policy %s: load existing snapshot: %w", binding.Duty, err)
		}

		row, err := qtx.GetRoleProfileVersionByKey(ctx, db.GetRoleProfileVersionByKeyParams{
			WorkspaceID: workspaceID, ProfileKey: binding.ProfileKey, Version: binding.Version,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, CommandValidationError{Errors: []ValidationError{{
				Path: "role_policy_bindings", Code: "role_profile_not_found",
				Message: fmt.Sprintf("RoleProfile %s version %d does not exist in the workspace", binding.ProfileKey, binding.Version),
			}}}
		}
		if err != nil {
			return nil, fmt.Errorf("freeze role policy %s: load RoleProfile: %w", binding.Duty, err)
		}
		profile, err := mapRoleProfileVersion(row)
		if err != nil {
			return nil, err
		}
		if profile.Duty != binding.Duty {
			return nil, CommandValidationError{Errors: []ValidationError{{
				Path: "role_policy_bindings", Code: "role_profile_duty_mismatch",
				Message: fmt.Sprintf("RoleProfile %s version %d belongs to duty %s, not %s", binding.ProfileKey, binding.Version, profile.Duty, binding.Duty),
			}}}
		}
		if binding.AgentID.Valid {
			if _, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: binding.AgentID, WorkspaceID: workspaceID}); errors.Is(err, pgx.ErrNoRows) {
				return nil, CommandValidationError{Errors: []ValidationError{{Path: "role_policy_bindings", Code: "agent_not_found", Message: "bound agent does not exist in the workspace"}}}
			} else if err != nil {
				return nil, fmt.Errorf("freeze role policy %s: validate agent binding: %w", binding.Duty, err)
			}
		}
		_, contentHash, err := rolePolicySnapshotContent(profile, binding)
		if err != nil {
			return nil, err
		}
		config, err := json.Marshal(profile.Config)
		if err != nil {
			return nil, fmt.Errorf("freeze role policy %s: encode config: %w", binding.Duty, err)
		}
		created, err := qtx.CreateMissionRolePolicySnapshot(ctx, db.CreateMissionRolePolicySnapshotParams{
			WorkspaceID: workspaceID, MissionID: missionID, Duty: binding.Duty.String(),
			RoleProfileID: row.ID, RoleProfileKey: profile.ProfileKey, RoleProfileVersion: profile.Version,
			ProfileName: profile.Name, ProfileDescription: profile.Description, Config: config,
			AgentID: binding.AgentID, SchemaVersion: RolePolicySnapshotSchemaVersion,
			ContentHash: contentHash, FrozenBy: actorID,
		})
		if err != nil {
			return nil, fmt.Errorf("freeze role policy %s: insert snapshot: %w", binding.Duty, err)
		}
		snapshot, err := mapRolePolicySnapshot(created)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Duty < snapshots[j].Duty })
	return snapshots, nil
}

func ensureFrozenRolePolicyBindingsMatch(
	ctx context.Context,
	queries *db.Queries,
	workspaceID, missionID pgtype.UUID,
	bindings []RolePolicyBinding,
) error {
	for _, binding := range bindings {
		row, err := queries.GetMissionRolePolicySnapshot(ctx, db.GetMissionRolePolicySnapshotParams{
			WorkspaceID: workspaceID, MissionID: missionID, Duty: binding.Duty.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCommandConflict
		}
		if err != nil {
			return fmt.Errorf("check frozen role policy %s: %w", binding.Duty, err)
		}
		snapshot, err := mapRolePolicySnapshot(row)
		if err != nil {
			return err
		}
		if !rolePolicyBindingMatchesSnapshot(binding, snapshot) {
			return ErrCommandConflict
		}
	}
	return nil
}

func mapRolePolicySnapshots(rows []db.MissionRolePolicySnapshot) ([]RolePolicySnapshot, error) {
	snapshots := make([]RolePolicySnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, err := mapRolePolicySnapshot(row)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func mapRolePolicySnapshot(row db.MissionRolePolicySnapshot) (RolePolicySnapshot, error) {
	if row.SchemaVersion != RolePolicySnapshotSchemaVersion {
		return RolePolicySnapshot{}, fmt.Errorf("decode role policy snapshot %s: unsupported schema version %d", uuidText(row.ID), row.SchemaVersion)
	}
	var config RoleProfileConfig
	if err := json.Unmarshal(row.Config, &config); err != nil {
		return RolePolicySnapshot{}, fmt.Errorf("decode role policy snapshot %s config: %w", uuidText(row.ID), err)
	}
	config, errs := normalizeAndValidateRoleProfileConfig(config)
	if len(errs) > 0 {
		return RolePolicySnapshot{}, fmt.Errorf("decode role policy snapshot %s config: stored config is invalid", uuidText(row.ID))
	}
	snapshot := RolePolicySnapshot{
		ID: uuidText(row.ID), WorkspaceID: uuidText(row.WorkspaceID), MissionID: uuidText(row.MissionID),
		SchemaVersion: row.SchemaVersion, Duty: Duty(row.Duty), RoleProfileID: uuidText(row.RoleProfileID),
		RoleProfileKey: row.RoleProfileKey, RoleProfileVersion: row.RoleProfileVersion,
		ProfileName: row.ProfileName, ProfileDescription: row.ProfileDescription, Config: config,
		ContentHash: row.ContentHash,
		FrozenBy:    uuidText(row.FrozenBy), FrozenAt: row.FrozenAt.Time,
	}
	snapshot.AgentID = optionalUUIDText(row.AgentID)
	if !snapshot.Duty.Valid() {
		return RolePolicySnapshot{}, fmt.Errorf("decode role policy snapshot %s: invalid duty %q", snapshot.ID, snapshot.Duty)
	}
	profile := RoleProfileVersion{
		ID: snapshot.RoleProfileID, ProfileKey: snapshot.RoleProfileKey, Version: snapshot.RoleProfileVersion,
		Duty: snapshot.Duty, Name: snapshot.ProfileName, Description: snapshot.ProfileDescription, Config: snapshot.Config,
	}
	binding := RolePolicyBinding{Duty: snapshot.Duty, ProfileKey: snapshot.RoleProfileKey, Version: snapshot.RoleProfileVersion, AgentID: row.AgentID}
	_, expectedHash, err := rolePolicySnapshotContent(profile, binding)
	if err != nil {
		return RolePolicySnapshot{}, err
	}
	if expectedHash != snapshot.ContentHash {
		return RolePolicySnapshot{}, fmt.Errorf("decode role policy snapshot %s: content hash mismatch", snapshot.ID)
	}
	return snapshot, nil
}

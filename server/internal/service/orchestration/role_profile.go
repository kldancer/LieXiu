package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

const (
	maxRoleProfileInstructionsBytes = 32 * 1024
	maxRoleProfileListItems         = 64
)

var roleProfileKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// RoleProfileConfig is the mutable template behind an immutable profile
// version. It configures execution policy only; Duty remains the complete set
// of state-machine responsibilities.
type RoleProfileConfig struct {
	Instructions         string                 `json:"instructions"`
	RequiredCapabilities []string               `json:"required_capabilities"`
	Runtime              RoleRuntimePreferences `json:"runtime"`
	Tools                RoleToolPermissions    `json:"tools"`
	Budget               RoleBudgetLimits       `json:"budget"`
	TimeoutSeconds       int                    `json:"timeout_seconds"`
	MaxConcurrency       int                    `json:"max_concurrency"`
}

type RoleRuntimePreferences struct {
	AllowedRuntimeIDs   []string `json:"allowed_runtime_ids"`
	PreferredRuntimeIDs []string `json:"preferred_runtime_ids"`
	Providers           []string `json:"providers"`
	Models              []string `json:"models"`
}

type RoleToolPermissions struct {
	AllowedTools []string `json:"allowed_tools"`
	AllowedPaths []string `json:"allowed_paths"`
}

type RoleBudgetLimits struct {
	MaxTokens           *int64 `json:"max_tokens,omitempty"`
	MaxCostUSDTicks     *int64 `json:"max_cost_usd_ticks,omitempty"`
	MaxReworkCycles     int    `json:"max_rework_cycles"`
	MaxTechnicalRetries int    `json:"max_technical_retries"`
}

type RoleProfileVersion struct {
	ID          string            `json:"id"`
	WorkspaceID string            `json:"workspace_id"`
	ProfileKey  string            `json:"profile_key"`
	Version     int32             `json:"version"`
	Duty        Duty              `json:"duty"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Config      RoleProfileConfig `json:"config"`
	CreatedBy   string            `json:"created_by"`
	CreatedAt   time.Time         `json:"created_at"`
}

type CreateRoleProfileVersionCommand struct {
	WorkspaceID pgtype.UUID
	CommandID   pgtype.UUID
	ActorID     pgtype.UUID
	ProfileKey  string
	Duty        Duty
	Name        string
	Description string
	Config      RoleProfileConfig
}

type CreateRoleProfileVersionResult struct {
	Profile    RoleProfileVersion `json:"profile"`
	Idempotent bool               `json:"idempotent"`
}

type CreateRoleProfileVersionParams struct {
	WorkspaceID pgtype.UUID
	CommandID   pgtype.UUID
	ActorID     pgtype.UUID
	ProfileKey  string
	Duty        Duty
	Name        string
	Description string
	Config      RoleProfileConfig
}

func (s *Service) CreateRoleProfileVersion(ctx context.Context, command CreateRoleProfileVersionCommand) (CreateRoleProfileVersionResult, error) {
	errs := validateCommandIdentity(command.WorkspaceID, pgtype.UUID{}, command.CommandID, pgtype.UUID{}, command.ActorID, false)
	key := strings.TrimSpace(command.ProfileKey)
	name := strings.TrimSpace(command.Name)
	description := strings.TrimSpace(command.Description)
	normalizedConfig, configErrs := normalizeAndValidateRoleProfileConfig(command.Config)
	errs = append(errs, validateRoleProfileIdentity(key, command.Duty, name, description)...)
	errs = append(errs, configErrs...)
	if len(errs) > 0 {
		return CreateRoleProfileVersionResult{}, CommandValidationError{Errors: errs}
	}
	if err := s.requireOwner(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return CreateRoleProfileVersionResult{}, err
	}
	return s.repository.CreateRoleProfileVersion(ctx, CreateRoleProfileVersionParams{
		WorkspaceID: command.WorkspaceID, CommandID: command.CommandID, ActorID: command.ActorID,
		ProfileKey: key, Duty: command.Duty, Name: name, Description: description, Config: normalizedConfig,
	})
}

func (s *Service) ListLatestRoleProfiles(ctx context.Context, workspaceID pgtype.UUID) ([]RoleProfileVersion, error) {
	if s == nil || s.queries == nil {
		return nil, fmt.Errorf("list role profiles: orchestration service is not configured")
	}
	if !validUUID(workspaceID) {
		return nil, CommandValidationError{Errors: []ValidationError{{Path: "workspace_id", Code: "invalid_uuid", Message: "workspace_id must be a non-zero UUID"}}}
	}
	rows, err := s.queries.ListLatestRoleProfiles(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list role profiles: %w", err)
	}
	return mapRoleProfileVersions(rows)
}

func (r *Repository) CreateRoleProfileVersion(ctx context.Context, params CreateRoleProfileVersionParams) (CreateRoleProfileVersionResult, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: repository is not configured")
	}
	config, err := json.Marshal(params.Config)
	if err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: encode config: %w", err)
	}
	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qtx := r.queries.WithTx(tx)
	if err := qtx.LockRoleProfileCommand(ctx, db.LockRoleProfileCommandParams{
		WorkspaceID: params.WorkspaceID, CommandID: params.CommandID,
	}); err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: lock command: %w", err)
	}

	existing, err := qtx.GetRoleProfileVersionByCommand(ctx, db.GetRoleProfileVersionByCommandParams{
		WorkspaceID: params.WorkspaceID, CommandID: params.CommandID,
	})
	if err == nil {
		profile, mapErr := mapRoleProfileVersion(existing)
		if mapErr != nil {
			return CreateRoleProfileVersionResult{}, mapErr
		}
		if profile.ProfileKey != params.ProfileKey || profile.Duty != params.Duty || profile.Name != params.Name || profile.Description != params.Description || !reflect.DeepEqual(profile.Config, params.Config) {
			return CreateRoleProfileVersionResult{}, ErrCommandConflict
		}
		return CreateRoleProfileVersionResult{Profile: profile, Idempotent: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: lookup command: %w", err)
	}
	if err := qtx.LockRoleProfileSeries(ctx, db.LockRoleProfileSeriesParams{
		WorkspaceID: params.WorkspaceID, ProfileKey: params.ProfileKey,
	}); err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: lock series: %w", err)
	}
	version, err := qtx.GetNextRoleProfileVersion(ctx, db.GetNextRoleProfileVersionParams{
		WorkspaceID: params.WorkspaceID, ProfileKey: params.ProfileKey,
	})
	if err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: allocate version: %w", err)
	}
	row, err := qtx.CreateRoleProfileVersion(ctx, db.CreateRoleProfileVersionParams{
		WorkspaceID: params.WorkspaceID, ProfileKey: params.ProfileKey, Version: version,
		Duty: string(params.Duty), Name: params.Name, Description: params.Description, Config: config,
		CommandID: params.CommandID, CreatedBy: params.ActorID,
	})
	if err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateRoleProfileVersionResult{}, fmt.Errorf("create role profile version: commit: %w", err)
	}
	profile, err := mapRoleProfileVersion(row)
	if err != nil {
		return CreateRoleProfileVersionResult{}, err
	}
	return CreateRoleProfileVersionResult{Profile: profile}, nil
}

func validateRoleProfileIdentity(key string, duty Duty, name, description string) []ValidationError {
	var errs []ValidationError
	if !roleProfileKeyPattern.MatchString(key) {
		errs = append(errs, ValidationError{Path: "profile_key", Code: "invalid_profile_key", Message: "profile_key must start with a lowercase letter and contain only lowercase letters, digits, underscores, or hyphens"})
	}
	if !duty.Valid() {
		errs = append(errs, ValidationError{Path: "duty", Code: "invalid_duty", Message: "duty must be planner, executor, reviewer, or integrator"})
	}
	if name == "" || utf8.RuneCountInString(name) > 120 {
		errs = append(errs, ValidationError{Path: "name", Code: "invalid_name", Message: "name must contain between 1 and 120 characters"})
	}
	if utf8.RuneCountInString(description) > 1000 {
		errs = append(errs, ValidationError{Path: "description", Code: "description_too_long", Message: "description must contain at most 1000 characters"})
	}
	return errs
}

func normalizeAndValidateRoleProfileConfig(config RoleProfileConfig) (RoleProfileConfig, []ValidationError) {
	var errs []ValidationError
	config.Instructions = strings.TrimSpace(config.Instructions)
	if len(config.Instructions) > maxRoleProfileInstructionsBytes {
		errs = append(errs, ValidationError{Path: "config.instructions", Code: "instructions_too_long", Message: "instructions must be at most 32768 bytes"})
	}
	lists := []struct {
		path          string
		values        *[]string
		preserveOrder bool
	}{
		{"config.required_capabilities", &config.RequiredCapabilities, false},
		{"config.runtime.allowed_runtime_ids", &config.Runtime.AllowedRuntimeIDs, false},
		// Preference order is semantic: the deterministic selector ranks the
		// first configured Runtime ahead of the next one. Keep insertion order
		// while still trimming and de-duplicating the list.
		{"config.runtime.preferred_runtime_ids", &config.Runtime.PreferredRuntimeIDs, true},
		{"config.runtime.providers", &config.Runtime.Providers, false},
		{"config.runtime.models", &config.Runtime.Models, false},
		{"config.tools.allowed_tools", &config.Tools.AllowedTools, false},
		{"config.tools.allowed_paths", &config.Tools.AllowedPaths, false},
	}
	for _, list := range lists {
		normalized, listErrs := normalizeRoleProfileList(list.path, *list.values, list.preserveOrder)
		*list.values = normalized
		errs = append(errs, listErrs...)
	}
	for _, runtimeList := range []struct {
		path   string
		values []string
	}{
		{"config.runtime.allowed_runtime_ids", config.Runtime.AllowedRuntimeIDs},
		{"config.runtime.preferred_runtime_ids", config.Runtime.PreferredRuntimeIDs},
	} {
		for _, id := range runtimeList.values {
			if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
				errs = append(errs, ValidationError{Path: runtimeList.path, Code: "invalid_runtime_id", Message: "runtime identifiers must be non-zero UUIDs"})
				break
			}
		}
	}
	if len(config.Runtime.AllowedRuntimeIDs) > 0 {
		allowed := make(map[string]struct{}, len(config.Runtime.AllowedRuntimeIDs))
		for _, id := range config.Runtime.AllowedRuntimeIDs {
			allowed[id] = struct{}{}
		}
		for _, id := range config.Runtime.PreferredRuntimeIDs {
			if _, ok := allowed[id]; !ok {
				errs = append(errs, ValidationError{Path: "config.runtime.preferred_runtime_ids", Code: "preferred_runtime_not_allowed", Message: "preferred runtimes must be included in allowed_runtime_ids when an allow-list is present"})
				break
			}
		}
	}
	if config.TimeoutSeconds < 1 || config.TimeoutSeconds > 86400 {
		errs = append(errs, ValidationError{Path: "config.timeout_seconds", Code: "invalid_timeout", Message: "timeout_seconds must be between 1 and 86400"})
	}
	if config.MaxConcurrency < 1 || config.MaxConcurrency > 32 {
		errs = append(errs, ValidationError{Path: "config.max_concurrency", Code: "invalid_concurrency", Message: "max_concurrency must be between 1 and 32"})
	}
	if config.Budget.MaxTokens != nil && *config.Budget.MaxTokens < 0 {
		errs = append(errs, ValidationError{Path: "config.budget.max_tokens", Code: "invalid_budget", Message: "max_tokens must not be negative"})
	}
	if config.Budget.MaxCostUSDTicks != nil && *config.Budget.MaxCostUSDTicks < 0 {
		errs = append(errs, ValidationError{Path: "config.budget.max_cost_usd_ticks", Code: "invalid_budget", Message: "max_cost_usd_ticks must not be negative"})
	}
	if config.Budget.MaxReworkCycles < 0 || config.Budget.MaxTechnicalRetries < 0 {
		errs = append(errs, ValidationError{Path: "config.budget", Code: "invalid_retry_budget", Message: "rework and technical retry budgets must not be negative"})
	}
	return config, errs
}

func normalizeRoleProfileList(path string, values []string, preserveOrder bool) ([]string, []ValidationError) {
	if len(values) > maxRoleProfileListItems {
		return nil, []ValidationError{{Path: path, Code: "too_many_items", Message: "list must contain at most 64 items"}}
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || len(value) > 512 {
			return nil, []ValidationError{{Path: path, Code: "invalid_item", Message: "list items must contain between 1 and 512 bytes"}}
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if !preserveOrder {
		sort.Strings(normalized)
	}
	if normalized == nil {
		normalized = []string{}
	}
	return normalized, nil
}

func mapRoleProfileVersions(rows []db.RoleProfile) ([]RoleProfileVersion, error) {
	profiles := make([]RoleProfileVersion, 0, len(rows))
	for _, row := range rows {
		profile, err := mapRoleProfileVersion(row)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func mapRoleProfileVersion(row db.RoleProfile) (RoleProfileVersion, error) {
	var config RoleProfileConfig
	if err := json.Unmarshal(row.Config, &config); err != nil {
		return RoleProfileVersion{}, fmt.Errorf("decode role profile %s config: %w", uuidText(row.ID), err)
	}
	normalized, errs := normalizeAndValidateRoleProfileConfig(config)
	if len(errs) > 0 {
		return RoleProfileVersion{}, fmt.Errorf("decode role profile %s config: stored config is invalid", uuidText(row.ID))
	}
	return RoleProfileVersion{
		ID: uuidText(row.ID), WorkspaceID: uuidText(row.WorkspaceID), ProfileKey: row.ProfileKey,
		Version: row.Version, Duty: Duty(row.Duty), Name: row.Name, Description: row.Description,
		Config: normalized, CreatedBy: uuidText(row.CreatedBy), CreatedAt: row.CreatedAt.Time,
	}, nil
}

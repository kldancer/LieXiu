package localinstance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

var (
	ErrSelectionRequired = errors.New("local owner and workspace selection is required")
	ErrInvalidSelection  = errors.New("selected user is not an owner of the selected workspace")
	ErrCorruptBinding    = errors.New("local instance binding is invalid")
	ErrIncompleteStore   = errors.New("existing identity data is incomplete")
	ErrInvalidInput      = errors.New("owner and workspace fields are required")
	ErrNotInitialized    = errors.New("local instance is not initialized")
)

type txStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Repository struct {
	queries   *db.Queries
	txStarter txStarter
}

type BootstrapInput struct {
	OwnerName     string
	OwnerEmail    string
	WorkspaceName string
	WorkspaceSlug string
	IssuePrefix   string
	WorkspaceID   string
}

type Result struct {
	Owner       db.User
	Workspace   db.Workspace
	Provisioned bool
}

type Status struct {
	Initialized       bool
	RequiresSelection bool
}

func NewRepository(queries *db.Queries, txStarter txStarter) *Repository {
	return &Repository{queries: queries, txStarter: txStarter}
}

func (r *Repository) Status(ctx context.Context) (Status, error) {
	if r == nil || r.queries == nil {
		return Status{}, fmt.Errorf("local instance repository is unavailable")
	}
	instance, err := r.queries.GetLocalInstance(ctx)
	if err == nil {
		if _, _, err := loadBoundIdentity(ctx, r.queries, instance); err != nil {
			return Status{}, err
		}
		return Status{Initialized: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Status{}, fmt.Errorf("read local instance: %w", err)
	}

	users, err := r.queries.CountLocalBootstrapUsers(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("count users: %w", err)
	}
	workspaces, err := r.queries.CountLocalBootstrapWorkspaces(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("count workspaces: %w", err)
	}
	owners, err := r.queries.CountLocalOwnerMemberships(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("count owners: %w", err)
	}

	if users == 0 && workspaces == 0 {
		return Status{}, nil
	}
	return Status{RequiresSelection: users == 0 || workspaces == 0 || owners != 1}, nil
}

// Current returns the validated canonical identity. It is the only read path
// clients may use after bootstrap; callers must still enforce that the
// authenticated actor is the returned owner.
func (r *Repository) Current(ctx context.Context) (Result, error) {
	if r == nil || r.queries == nil {
		return Result{}, fmt.Errorf("local instance repository is unavailable")
	}
	instance, err := r.queries.GetLocalInstance(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, ErrNotInitialized
		}
		return Result{}, fmt.Errorf("read local instance: %w", err)
	}
	owner, workspace, err := loadBoundIdentity(ctx, r.queries, instance)
	if err != nil {
		return Result{}, err
	}
	return Result{Owner: owner, Workspace: workspace}, nil
}

func (r *Repository) Bootstrap(ctx context.Context, input BootstrapInput) (Result, error) {
	return r.bootstrap(ctx, input, false)
}

// BootstrapPersonal provisions defaults only for an empty store and otherwise
// binds the sole owner membership without using those defaults as a selector.
// It is intended for the separately gated localhost personal-mode handler.
func (r *Repository) BootstrapPersonal(ctx context.Context, defaults BootstrapInput) (Result, error) {
	return r.bootstrap(ctx, defaults, true)
}

func (r *Repository) bootstrap(ctx context.Context, input BootstrapInput, autoSelect bool) (Result, error) {
	if r == nil || r.queries == nil || r.txStarter == nil {
		return Result{}, fmt.Errorf("local instance repository is unavailable")
	}
	input.OwnerName = strings.TrimSpace(input.OwnerName)
	input.OwnerEmail = strings.ToLower(strings.TrimSpace(input.OwnerEmail))
	input.WorkspaceName = strings.TrimSpace(input.WorkspaceName)
	input.WorkspaceSlug = strings.ToLower(strings.TrimSpace(input.WorkspaceSlug))
	input.IssuePrefix = strings.ToUpper(strings.TrimSpace(input.IssuePrefix))
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)

	tx, err := r.txStarter.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin local bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := r.queries.WithTx(tx)

	if err := qtx.LockLocalInstanceBootstrap(ctx); err != nil {
		return Result{}, fmt.Errorf("lock local bootstrap: %w", err)
	}

	instance, err := qtx.GetLocalInstance(ctx)
	if err == nil {
		owner, workspace, err := loadBoundIdentity(ctx, qtx, instance)
		if err != nil {
			return Result{}, err
		}
		owner, err = ensureOwnerOnboarded(ctx, qtx, owner)
		if err != nil {
			return Result{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit local bootstrap replay: %w", err)
		}
		return Result{Owner: owner, Workspace: workspace}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, fmt.Errorf("read local instance: %w", err)
	}

	users, err := qtx.CountLocalBootstrapUsers(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("count users: %w", err)
	}
	workspaces, err := qtx.CountLocalBootstrapWorkspaces(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("count workspaces: %w", err)
	}

	var result Result
	switch {
	case users == 0 && workspaces == 0:
		result, err = provisionEmptyStore(ctx, qtx, input)
	case users == 0 || workspaces == 0:
		err = ErrIncompleteStore
	default:
		if autoSelect {
			result, err = bindUniqueExistingStore(ctx, qtx)
		} else {
			result, err = bindExistingStore(ctx, qtx, input)
		}
	}
	if err != nil {
		return Result{}, err
	}
	result.Owner, err = ensureOwnerOnboarded(ctx, qtx, result.Owner)
	if err != nil {
		return Result{}, err
	}

	if _, err := qtx.CreateLocalInstance(ctx, db.CreateLocalInstanceParams{
		OwnerUserID:          result.Owner.ID,
		CanonicalWorkspaceID: result.Workspace.ID,
	}); err != nil {
		return Result{}, fmt.Errorf("create local instance: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit local bootstrap: %w", err)
	}
	return result, nil
}

func bindUniqueExistingStore(ctx context.Context, q *db.Queries) (Result, error) {
	candidates, err := q.ListLocalOwnerCandidates(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list local owner candidates: %w", err)
	}
	if len(candidates) != 1 {
		return Result{}, ErrSelectionRequired
	}
	owner, err := q.GetUser(ctx, candidates[0].UserID)
	if err != nil {
		return Result{}, fmt.Errorf("load selected owner: %w", err)
	}
	workspace, err := q.GetWorkspace(ctx, candidates[0].WorkspaceID)
	if err != nil {
		return Result{}, fmt.Errorf("load selected workspace: %w", err)
	}
	return Result{Owner: owner, Workspace: workspace}, nil
}

func provisionEmptyStore(ctx context.Context, q *db.Queries, input BootstrapInput) (Result, error) {
	if input.OwnerName == "" || input.OwnerEmail == "" || input.WorkspaceName == "" || input.WorkspaceSlug == "" || input.IssuePrefix == "" {
		return Result{}, ErrInvalidInput
	}

	owner, err := q.CreateUser(ctx, db.CreateUserParams{Name: input.OwnerName, Email: input.OwnerEmail})
	if err != nil {
		return Result{}, fmt.Errorf("create local owner: %w", err)
	}
	workspace, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:        input.WorkspaceName,
		Slug:        input.WorkspaceSlug,
		IssuePrefix: input.IssuePrefix,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create local workspace: %w", err)
	}
	if _, err := q.CreateMember(ctx, db.CreateMemberParams{
		WorkspaceID: workspace.ID,
		UserID:      owner.ID,
		Role:        "owner",
	}); err != nil {
		return Result{}, fmt.Errorf("create local owner membership: %w", err)
	}
	return Result{Owner: owner, Workspace: workspace, Provisioned: true}, nil
}

func bindExistingStore(ctx context.Context, q *db.Queries, input BootstrapInput) (Result, error) {
	var ownerID, workspaceID pgtype.UUID
	if input.OwnerEmail == "" {
		return Result{}, ErrSelectionRequired
	} else if input.WorkspaceID != "" {
		parsedWorkspaceID, err := parseUUID(input.WorkspaceID)
		if err != nil {
			return Result{}, ErrInvalidSelection
		}
		candidate, err := q.GetLocalOwnerCandidate(ctx, db.GetLocalOwnerCandidateParams{
			Lower: input.OwnerEmail,
			ID:    parsedWorkspaceID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Result{}, ErrInvalidSelection
			}
			return Result{}, fmt.Errorf("get local owner selection: %w", err)
		}
		ownerID, workspaceID = candidate.UserID, candidate.WorkspaceID
	} else {
		candidates, err := q.ListLocalOwnerCandidatesByEmail(ctx, input.OwnerEmail)
		if err != nil {
			return Result{}, fmt.Errorf("list local owner candidates: %w", err)
		}
		if len(candidates) != 1 {
			return Result{}, ErrSelectionRequired
		}
		ownerID, workspaceID = candidates[0].UserID, candidates[0].WorkspaceID
	}

	owner, err := q.GetUser(ctx, ownerID)
	if err != nil {
		return Result{}, fmt.Errorf("load selected owner: %w", err)
	}
	workspace, err := q.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return Result{}, fmt.Errorf("load selected workspace: %w", err)
	}
	return Result{Owner: owner, Workspace: workspace}, nil
}

func loadBoundIdentity(ctx context.Context, q *db.Queries, instance db.LocalInstance) (db.User, db.Workspace, error) {
	owner, err := q.GetUser(ctx, instance.OwnerUserID)
	if err != nil {
		return db.User{}, db.Workspace{}, fmt.Errorf("%w: owner: %v", ErrCorruptBinding, err)
	}
	workspace, err := q.GetWorkspace(ctx, instance.CanonicalWorkspaceID)
	if err != nil {
		return db.User{}, db.Workspace{}, fmt.Errorf("%w: workspace: %v", ErrCorruptBinding, err)
	}
	membership, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      instance.OwnerUserID,
		WorkspaceID: instance.CanonicalWorkspaceID,
	})
	if err != nil || membership.Role != "owner" {
		return db.User{}, db.Workspace{}, fmt.Errorf("%w: owner membership", ErrCorruptBinding)
	}
	return owner, workspace, nil
}

func ensureOwnerOnboarded(ctx context.Context, q *db.Queries, owner db.User) (db.User, error) {
	if owner.OnboardedAt.Valid {
		return owner, nil
	}
	updated, err := q.MarkUserOnboarded(ctx, owner.ID)
	if err != nil {
		return db.User{}, fmt.Errorf("mark local owner onboarded: %w", err)
	}
	return updated, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return pgtype.UUID{}, fmt.Errorf("invalid uuid")
	}
	return id, nil
}

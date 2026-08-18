package localinstance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

type localIdentityFixture struct {
	userID      pgtype.UUID
	email       string
	workspaceID []pgtype.UUID
}

func newLocalInstancePool(t *testing.T) (*pgxpool.Pool, *db.Queries) {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL not set; refusing to connect to a default database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("PostgreSQL is not reachable: %v", err)
	}

	schema := "localinstance_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", quotedSchema)); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quotedSchema)); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	})

	for _, table := range []string{"user", "workspace", "member", "local_instance"} {
		qualifiedTable := pgx.Identifier{schema, table}.Sanitize()
		publicTable := pgx.Identifier{"public", table}.Sanitize()
		statement := fmt.Sprintf("CREATE TABLE %s (LIKE %s INCLUDING DEFAULTS)", qualifiedTable, publicTable)
		if _, err := adminPool.Exec(ctx, statement); err != nil {
			t.Fatalf("create isolated %s table: %v", table, err)
		}
	}
	if _, err := adminPool.Exec(ctx, fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT local_instance_singleton_pk PRIMARY KEY (singleton_key)",
		pgx.Identifier{schema, "local_instance"}.Sanitize(),
	)); err != nil {
		t.Fatalf("add isolated local_instance primary key: %v", err)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL config: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", quotedSchema))
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create isolated PostgreSQL pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping isolated PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
	})
	return pool, db.New(pool)
}

func requireUnboundLocalInstance(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var bound bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM local_instance WHERE singleton_key = TRUE)`).Scan(&bound); err != nil {
		t.Fatalf("check local instance binding: %v", err)
	}
	if bound {
		t.Fatal("isolated local_instance unexpectedly already bound")
	}
}

func emptyStore(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var users, workspaces, owners int64
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "user"`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspace`).Scan(&workspaces); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE role = 'owner'`).Scan(&owners); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if users != 0 || workspaces != 0 || owners != 0 {
		t.Fatalf("isolated database is not empty (users=%d workspaces=%d owners=%d)", users, workspaces, owners)
	}
}

func seedIdentityFixture(t *testing.T, pool *pgxpool.Pool, workspaceCount int, role string) localIdentityFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	fixture := localIdentityFixture{email: "local-instance-" + suffix + "@liexiu.test"}
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Local Instance Test Owner", fixture.email).Scan(&fixture.userID); err != nil {
		t.Fatalf("create fixture user: %v", err)
	}

	for i := 0; i < workspaceCount; i++ {
		var workspaceID pgtype.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO workspace (name, slug, description, issue_prefix)
			VALUES ($1, $2, '', $3)
			RETURNING id
		`, fmt.Sprintf("Local Instance Test Workspace %d", i), "local-instance-"+suffix+fmt.Sprintf("-%d", i), "LIT").Scan(&workspaceID); err != nil {
			t.Fatalf("create fixture workspace: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO member (workspace_id, user_id, role)
			VALUES ($1, $2, $3)
		`, workspaceID, fixture.userID, role); err != nil {
			t.Fatalf("create fixture membership: %v", err)
		}
		fixture.workspaceID = append(fixture.workspaceID, workspaceID)
	}

	t.Cleanup(func() {
		cleanupIdentityFixture(t, pool, fixture)
	})
	return fixture
}

func cleanupIdentityFixture(t *testing.T, pool *pgxpool.Pool, fixture localIdentityFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM local_instance WHERE owner_user_id = $1`, fixture.userID); err != nil {
		t.Errorf("cleanup local_instance: %v", err)
	}
	for _, workspaceID := range fixture.workspaceID {
		if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, workspaceID); err != nil {
			t.Errorf("cleanup workspace: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, fixture.userID); err != nil {
		t.Errorf("cleanup user: %v", err)
	}
}

func bootstrapInputFor(fixture localIdentityFixture) BootstrapInput {
	return BootstrapInput{
		OwnerName:     "Local Instance Test Owner",
		OwnerEmail:    fixture.email,
		WorkspaceName: "Local Instance Test Workspace",
		WorkspaceSlug: "local-instance-test",
		IssuePrefix:   "LIT",
	}
}

func TestRepositoryBootstrapEmptyStoreIsIdempotent(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	emptyStore(t, pool)

	repository := NewRepository(queries, pool)
	result, err := repository.Bootstrap(context.Background(), BootstrapInput{
		OwnerName:     "Local Bootstrap Owner",
		OwnerEmail:    "local-bootstrap-owner@liexiu.test",
		WorkspaceName: "Local Bootstrap Workspace",
		WorkspaceSlug: "local-bootstrap",
		IssuePrefix:   "LBS",
	})
	if err != nil {
		t.Fatalf("bootstrap empty store: %v", err)
	}
	if !result.Provisioned {
		t.Fatal("first bootstrap should provision the empty store")
	}
	if !result.Owner.OnboardedAt.Valid {
		t.Fatal("bootstrap owner should be marked onboarded")
	}

	replay, err := repository.Bootstrap(context.Background(), BootstrapInput{})
	if err != nil {
		t.Fatalf("replay bootstrap: %v", err)
	}
	if replay.Provisioned {
		t.Fatal("bootstrap replay must not provision a second identity")
	}
	if !replay.Owner.OnboardedAt.Valid {
		t.Fatal("replayed bootstrap owner should remain onboarded")
	}
	if replay.Owner.ID != result.Owner.ID || replay.Workspace.ID != result.Workspace.ID {
		t.Fatalf("replay changed canonical identity: first=(%v,%v) replay=(%v,%v)", result.Owner.ID, result.Workspace.ID, replay.Owner.ID, replay.Workspace.ID)
	}

	var users, workspaces, owners, instances int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM "user"`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workspace`).Scan(&workspaces); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM member WHERE role = 'owner'`).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM local_instance`).Scan(&instances); err != nil {
		t.Fatal(err)
	}
	if users != 1 || workspaces != 1 || owners != 1 || instances != 1 {
		t.Fatalf("unexpected bootstrap cardinality: users=%d workspaces=%d owners=%d instances=%d", users, workspaces, owners, instances)
	}
	cleanupIdentityFixture(t, pool, localIdentityFixture{userID: result.Owner.ID, workspaceID: []pgtype.UUID{result.Workspace.ID}})
}

func TestRepositoryCurrentReturnsOnlyValidatedBinding(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	repository := NewRepository(queries, pool)

	if _, err := repository.Current(context.Background()); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Current before bootstrap error = %v, want ErrNotInitialized", err)
	}

	result, err := repository.Bootstrap(context.Background(), BootstrapInput{
		OwnerName:     "Canonical Owner",
		OwnerEmail:    "canonical-owner@liexiu.test",
		WorkspaceName: "Canonical Workspace",
		WorkspaceSlug: "canonical-workspace",
		IssuePrefix:   "CAN",
	})
	if err != nil {
		t.Fatalf("bootstrap canonical identity: %v", err)
	}
	current, err := repository.Current(context.Background())
	if err != nil {
		t.Fatalf("Current after bootstrap: %v", err)
	}
	if current.Owner.ID != result.Owner.ID || current.Workspace.ID != result.Workspace.ID {
		t.Fatalf("Current changed binding: got=(%v,%v) want=(%v,%v)", current.Owner.ID, current.Workspace.ID, result.Owner.ID, result.Workspace.ID)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE member SET role='member' WHERE user_id=$1 AND workspace_id=$2`, result.Owner.ID, result.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Current(context.Background()); !errors.Is(err, ErrCorruptBinding) {
		t.Fatalf("Current with downgraded owner error = %v, want ErrCorruptBinding", err)
	}
	cleanupIdentityFixture(t, pool, localIdentityFixture{userID: result.Owner.ID, workspaceID: []pgtype.UUID{result.Workspace.ID}})
}

func TestRepositoryBootstrapConcurrentEmptyStoreCreatesOneBinding(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	emptyStore(t, pool)

	repository := NewRepository(queries, pool)
	input := BootstrapInput{
		OwnerName:     "Concurrent Bootstrap Owner",
		OwnerEmail:    "concurrent-bootstrap-owner@liexiu.test",
		WorkspaceName: "Concurrent Bootstrap Workspace",
		WorkspaceSlug: "concurrent-bootstrap",
		IssuePrefix:   "CBS",
	}

	start := make(chan struct{})
	results := make(chan Result, 2)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := repository.Bootstrap(context.Background(), input)
			results <- result
			errorsCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)

	var observed []Result
	for result := range results {
		observed = append(observed, result)
	}
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent bootstrap: %v", err)
		}
	}
	if len(observed) != 2 || observed[0].Provisioned == observed[1].Provisioned {
		t.Fatalf("expected exactly one provisioning result: %+v", observed)
	}
	if observed[0].Owner.ID != observed[1].Owner.ID || observed[0].Workspace.ID != observed[1].Workspace.ID {
		t.Fatalf("concurrent bootstrap returned different bindings: %+v", observed)
	}

	var users, workspaces, owners, instances int
	ctx := context.Background()
	for query, target := range map[string]*int{
		`SELECT count(*) FROM "user"`:                      &users,
		`SELECT count(*) FROM workspace`:                   &workspaces,
		`SELECT count(*) FROM member WHERE role = 'owner'`: &owners,
		`SELECT count(*) FROM local_instance`:              &instances,
	} {
		if err := pool.QueryRow(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if users != 1 || workspaces != 1 || owners != 1 || instances != 1 {
		t.Fatalf("concurrent bootstrap over-provisioned: users=%d workspaces=%d owners=%d instances=%d", users, workspaces, owners, instances)
	}
	cleanupIdentityFixture(t, pool, localIdentityFixture{userID: observed[0].Owner.ID, workspaceID: []pgtype.UUID{observed[0].Workspace.ID}})
}

func TestRepositoryBootstrapBindsUniqueExistingOwner(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	fixture := seedIdentityFixture(t, pool, 1, "owner")

	result, err := NewRepository(queries, pool).Bootstrap(context.Background(), BootstrapInput{
		OwnerEmail: fixture.email,
	})
	if err != nil {
		t.Fatalf("bind unique existing owner: %v", err)
	}
	if result.Provisioned {
		t.Fatal("binding an existing owner must not provision a new identity")
	}
	if !result.Owner.OnboardedAt.Valid {
		t.Fatal("bound owner should be marked onboarded")
	}
	if result.Owner.ID != fixture.userID || result.Workspace.ID != fixture.workspaceID[0] {
		t.Fatalf("bound the wrong identity: owner=%v workspace=%v", result.Owner.ID, result.Workspace.ID)
	}
	status, err := NewRepository(queries, pool).Status(context.Background())
	if err != nil {
		t.Fatalf("status after binding: %v", err)
	}
	if !status.Initialized || status.RequiresSelection {
		t.Fatalf("status after binding = %+v, want initialized canonical identity", status)
	}
}

func TestRepositoryBootstrapAutoBindsOnlyExistingOwner(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	fixture := seedIdentityFixture(t, pool, 1, "owner")

	result, err := NewRepository(queries, pool).BootstrapPersonal(context.Background(), BootstrapInput{
		OwnerName:     "LieXiu Owner",
		OwnerEmail:    "owner@liexiu.local",
		WorkspaceName: "LieXiu",
		WorkspaceSlug: "liexiu",
		IssuePrefix:   "LX",
	})
	if err != nil {
		t.Fatalf("auto-bind unique existing owner: %v", err)
	}
	if result.Provisioned {
		t.Fatal("auto-binding an existing owner must not provision a new identity")
	}
	if result.Owner.ID != fixture.userID || result.Workspace.ID != fixture.workspaceID[0] {
		t.Fatalf("auto-bind selected (%v, %v), want (%v, %v)", result.Owner.ID, result.Workspace.ID, fixture.userID, fixture.workspaceID[0])
	}
}

func TestRepositoryBootstrapAutoBindFailsClosedWhenOwnerIsAmbiguous(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	seedIdentityFixture(t, pool, 2, "owner")

	_, err := NewRepository(queries, pool).BootstrapPersonal(context.Background(), BootstrapInput{
		OwnerName:     "LieXiu Owner",
		OwnerEmail:    "owner@liexiu.local",
		WorkspaceName: "LieXiu",
		WorkspaceSlug: "liexiu",
		IssuePrefix:   "LX",
	})
	if !errors.Is(err, ErrSelectionRequired) {
		t.Fatalf("ambiguous auto-bind error = %v, want ErrSelectionRequired", err)
	}
	requireUnboundLocalInstance(t, pool)
}

func TestRepositoryBootstrapRequiresExplicitSelectionForMultipleOwners(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	fixture := seedIdentityFixture(t, pool, 2, "owner")
	repository := NewRepository(queries, pool)

	_, err := repository.Bootstrap(context.Background(), BootstrapInput{OwnerEmail: fixture.email})
	if !errors.Is(err, ErrSelectionRequired) {
		t.Fatalf("missing workspace selection error = %v, want ErrSelectionRequired", err)
	}

	result, err := repository.Bootstrap(context.Background(), BootstrapInput{
		OwnerEmail:  fixture.email,
		WorkspaceID: uuidText(fixture.workspaceID[1]),
	})
	if err != nil {
		t.Fatalf("explicit workspace selection: %v", err)
	}
	if result.Workspace.ID != fixture.workspaceID[1] {
		t.Fatalf("selected workspace %v, got %v", fixture.workspaceID[1], result.Workspace.ID)
	}
}

func TestRepositoryBootstrapRejectsNonOwnerSelection(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	fixture := seedIdentityFixture(t, pool, 1, "member")

	_, err := NewRepository(queries, pool).Bootstrap(context.Background(), BootstrapInput{
		OwnerEmail:  fixture.email,
		WorkspaceID: uuidText(fixture.workspaceID[0]),
	})
	if !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("non-owner selection error = %v, want ErrInvalidSelection", err)
	}
}

func TestRepositoryBootstrapRejectsCorruptSingleton(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	fixture := seedIdentityFixture(t, pool, 1, "owner")
	var missingWorkspace pgtype.UUID
	if err := missingWorkspace.Scan(uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO local_instance (singleton_key, owner_user_id, canonical_workspace_id, bootstrap_version)
		VALUES (TRUE, $1, $2, 1)
	`, fixture.userID, missingWorkspace); err != nil {
		t.Fatalf("seed corrupt singleton: %v", err)
	}

	_, err := NewRepository(queries, pool).Bootstrap(context.Background(), BootstrapInput{})
	if !errors.Is(err, ErrCorruptBinding) {
		t.Fatalf("corrupt singleton error = %v, want ErrCorruptBinding", err)
	}
}

func TestRepositoryStatusReportsBindingAndAmbiguity(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	var baselineOwners int64
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM member WHERE role = 'owner'`).Scan(&baselineOwners); err != nil {
		t.Fatalf("count baseline owners: %v", err)
	}
	fixture := seedIdentityFixture(t, pool, 1, "owner")

	status, err := NewRepository(queries, pool).Status(context.Background())
	if err != nil {
		t.Fatalf("status for unique existing owner: %v", err)
	}
	wantSelection := baselineOwners+1 != 1
	if status.Initialized || status.RequiresSelection != wantSelection {
		t.Fatalf("unique existing owner status = %+v, want initialized=false requires_selection=%t (baseline owners=%d)", status, wantSelection, baselineOwners)
	}
	cleanupIdentityFixture(t, pool, fixture)

	fixture = seedIdentityFixture(t, pool, 2, "owner")
	status, err = NewRepository(queries, pool).Status(context.Background())
	if err != nil {
		t.Fatalf("status for ambiguous existing owners: %v", err)
	}
	if status.Initialized || !status.RequiresSelection {
		t.Fatalf("ambiguous existing owner status = %+v, want selection required", status)
	}
}

type commitFailureStarter struct {
	pool *pgxpool.Pool
}

func (s commitFailureStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return commitFailureTx{Tx: tx}, nil
}

type commitFailureTx struct {
	pgx.Tx
}

func (commitFailureTx) Commit(context.Context) error {
	return errors.New("forced local bootstrap commit failure")
}

func TestRepositoryBootstrapRollsBackOnCommitFailure(t *testing.T) {
	pool, queries := newLocalInstancePool(t)
	requireUnboundLocalInstance(t, pool)
	emptyStore(t, pool)

	_, err := NewRepository(queries, commitFailureStarter{pool: pool}).Bootstrap(context.Background(), BootstrapInput{
		OwnerName:     "Rollback Owner",
		OwnerEmail:    "rollback-owner@liexiu.test",
		WorkspaceName: "Rollback Workspace",
		WorkspaceSlug: "rollback-workspace",
		IssuePrefix:   "RBK",
	})
	if err == nil {
		t.Fatal("bootstrap should report a commit failure")
	}

	ctx := context.Background()
	for query, want := range map[string]int{
		`SELECT count(*) FROM "user"`:                      0,
		`SELECT count(*) FROM workspace`:                   0,
		`SELECT count(*) FROM member WHERE role = 'owner'`: 0,
		`SELECT count(*) FROM local_instance`:              0,
	} {
		var got int
		if err := pool.QueryRow(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("after rollback %q = %d, want %d", query, got, want)
		}
	}
}

func uuidText(id pgtype.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}

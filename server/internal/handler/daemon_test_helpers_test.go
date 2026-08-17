package handler

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
)

// mockDB is the minimal DBTX used by daemon error-path tests. It deliberately
// stays independent of the removed email/signup test fixture.
type mockDB struct {
	db.DBTX
	getUserErr error
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return &mockRow{err: m.getUserErr}
}

func (m *mockDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 1"), nil
}

type mockRow struct {
	pgx.Row
	err error
}

func (m *mockRow) Scan(dest ...interface{}) error {
	return m.err
}

package service

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// TxStarter is the shared transaction boundary used by service constructors.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

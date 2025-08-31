package repo

import (
	"context"
	"database/sql"
	"errors"
)

var ErrWalletNotFound = errors.New("wallet not found")

type Repo interface {
	GetBalance(ctx context.Context, address string) (int64, error)
}

type PostgresRepo struct {
	DB *sql.DB
}

func NewPostgres(db *sql.DB) *PostgresRepo {
	return &PostgresRepo{DB: db}
}

func (r *PostgresRepo) GetBalance(ctx context.Context, address string) (int64, error) {
	const q = `SELECT balance_cents FROM wallets WHERE address=$1`
	var cents int64
	err := r.DB.QueryRowContext(ctx, q, address).Scan(&cents)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrWalletNotFound
		}
		return 0, err
	}
	return cents, nil
}

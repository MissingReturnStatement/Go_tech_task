package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"math/rand"

	"github.com/jackc/pgx/v5/pgconn"
)

type Transaction struct {
	ID          int64
	FromAddress string
	ToAddress   string
	AmountCents int64
	CreatedAt   time.Time
}

var (
	ErrWalletNotFound    = errors.New("wallet not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrSameAddress       = errors.New("from == to")
)

type Repo interface {
	GetBalance(ctx context.Context, address string) (int64, error)
	Transfer(ctx context.Context, from, to string, amountCents int64) error
	GetLastTransactions(ctx context.Context, n int) ([]Transaction, error) 
}

func (r *PostgresRepo) GetLastTransactions(ctx context.Context, n int) ([]Transaction, error) {
	if n <= 0 {
		n = 10
	}
	if n > 100 {
		n = 100
	}

	rows, err := r.DB.QueryContext(ctx, `
		SELECT id, from_address, to_address, amount_cents, created_at
		FROM transactions
		ORDER BY created_at DESC
		LIMIT $1
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.FromAddress, &t.ToAddress, &t.AmountCents, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}


type PostgresRepo struct{ DB *sql.DB }

func NewPostgres(db *sql.DB) *PostgresRepo { return &PostgresRepo{DB: db} }

func (r *PostgresRepo) GetBalance(ctx context.Context, address string) (int64, error) {
	const q = `SELECT balance_cents FROM wallets WHERE address=$1`
	var cents int64
	if err := r.DB.QueryRowContext(ctx, q, address).Scan(&cents); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrWalletNotFound
		}
		return 0, err
	}
	return cents, nil
}


func isDeadlock(err error) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == "40P01"
}

func (r *PostgresRepo) transferOnce(ctx context.Context, from, to string, amountCents int64) error {
	if from == to {
		return ErrSameAddress
	}
	if amountCents <= 0 {
		return errors.New("amount must be > 0")
	}

	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	a1, a2 := from, to
	swap := false
	if a2 < a1 {
		a1, a2 = a2, a1
		swap = true
	}

	type row struct {
		addr string
		bal  int64
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT address, balance_cents
		FROM wallets
		WHERE address = $1 OR address = $2
		ORDER BY address
		FOR UPDATE
	`, a1, a2)
	if err != nil {
		return err
	}
	defer rows.Close()

	var got []row
	for rows.Next() {
		var rrow row
		if err := rows.Scan(&rrow.addr, &rrow.bal); err != nil {
			return err
		}
		got = append(got, rrow)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(got) != 2 {
		return ErrWalletNotFound
	}

	var fromBal, toBal int64
	if !swap {
		if got[0].addr != from || got[1].addr != to {
		}
		fromBal = got[0].bal
		toBal = got[1].bal
	} else {
		fromBal = got[1].bal
		toBal = got[0].bal
	}

	if fromBal < amountCents {
		return ErrInsufficientFunds
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET balance_cents = $1 WHERE address = $2`,
		fromBal-amountCents, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET balance_cents = $1 WHERE address = $2`,
		toBal+amountCents, to); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO transactions(from_address, to_address, amount_cents)
		VALUES ($1, $2, $3)
	`, from, to, amountCents); err != nil {
		return err
	}

	return tx.Commit()
}

func (r *PostgresRepo) Transfer(ctx context.Context, from, to string, amountCents int64) error {
    const maxAttempts = 10 

    for attempt := 0; attempt < maxAttempts; attempt++ {
        err := r.transferOnce(ctx, from, to, amountCents)
        if err == nil {
            return nil
        }
        if isDeadlock(err) {
            backoff := time.Duration(15*(attempt+1)) * time.Millisecond
            jitter  := time.Duration(rand.Intn(15)) * time.Millisecond 
            sleep := backoff + jitter

            select {
            case <-time.After(sleep):
                continue
            case <-ctx.Done():
                return ctx.Err()
            }
        }
        return err
    }
    return errors.New("could not complete transfer after retries")
}


package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

const defaultWallets = 10
const defaultBalanceCents int64 = 10000 // 100.00

func SeedInitialWallets(db *sql.DB) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallets`).Scan(&n); err != nil {
		return nil, fmt.Errorf("seed count wallets: %w", err)
	}
	if n > 0 {
		return nil, nil 
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("seed begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO wallets(address, balance_cents) VALUES ($1,$2)`)
	if err != nil {
		return nil, fmt.Errorf("seed prepare: %w", err)
	}
	defer stmt.Close()

	addrs := make([]string, 0, defaultWallets)
	for i := 0; i < defaultWallets; i++ {
		addr, err := randomHex(32) 
		if err != nil {
			return nil, fmt.Errorf("seed random addr: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, addr, defaultBalanceCents); err != nil {
			return nil, fmt.Errorf("seed insert: %w", err)
		}
		addrs = append(addrs, addr)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("seed commit: %w", err)
	}
	return addrs, nil
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

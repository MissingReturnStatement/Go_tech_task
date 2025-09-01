package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// defaultWallets, количество кошельков создаваемых при инициализации
const defaultWallets = 10

// defaultBalanceCents, стартовый баланс в центах для каждого кошелька
const defaultBalanceCents int64 = 10000 // 100.00

// SeedInitialWallets, инициализирует таблицу кошельков начальными данными если она пуста, возвращает список созданных адресов или nil если записи уже есть
func SeedInitialWallets(db *sql.DB) ([]string, error) {
	// ограничиваем время операции
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// проверяем есть ли уже кошельки в таблице
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wallets`).Scan(&n); err != nil {
		return nil, fmt.Errorf("seed count wallets: %w", err)
	}
	if n > 0 {
		// таблица не пуста, ничего не делаем
		return nil, nil
	}

	// начинаем транзакцию на запись
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("seed begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// подготавливаем выражение вставки
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO wallets(address, balance_cents) VALUES ($1,$2)`)
	if err != nil {
		return nil, fmt.Errorf("seed prepare: %w", err)
	}
	defer stmt.Close()

	// генерируем адреса и вставляем записи с одинаковым балансом
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

	// фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("seed commit: %w", err)
	}
	return addrs, nil
}

// randomHex, возвращает случайную строку hex длиной nBytes байт
func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

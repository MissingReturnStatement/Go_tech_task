package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"gotechtask/internal/repo"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@127.0.0.1:5433/wallet_service?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	return db
}

func buildRouter(db *sql.DB) http.Handler {
	r := chi.NewRouter()
	api := &API{Repo: repo.NewPostgres(db)}
	api.Routes(r)
	return r
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func createWallet(t *testing.T, db *sql.DB, cents int64) string {
	t.Helper()
	addr := randHex(32)
	if _, err := db.Exec(`INSERT INTO wallets(address, balance_cents) VALUES ($1,$2)`, addr, cents); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	return addr
}

func getBalance(t *testing.T, db *sql.DB, addr string) int64 {
	t.Helper()
	var c int64
	if err := db.QueryRow(`SELECT balance_cents FROM wallets WHERE address=$1`, addr).Scan(&c); err != nil {
		t.Fatalf("select balance: %v", err)
	}
	return c
}

func cleanupWallets(t *testing.T, db *sql.DB, addrs ...string) {
	t.Helper()
	for _, a := range addrs {
		_, _ = db.Exec(`DELETE FROM transactions WHERE from_address=$1 OR to_address=$1`, a)
		_, _ = db.Exec(`DELETE FROM wallets WHERE address=$1`, a)
	}
}

func TestSend_Success(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	from := createWallet(t, db, 10000)
	to := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, from, to)

	beforeFrom := getBalance(t, db, from)
	beforeTo := getBalance(t, db, to)

	r := buildRouter(db)

	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":3.50}`, from, to)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	afterFrom := getBalance(t, db, from)
	afterTo := getBalance(t, db, to)

	if afterFrom != beforeFrom-350 {
		t.Fatalf("from balance mismatch: want %d got %d", beforeFrom-350, afterFrom)
	}
	if afterTo != beforeTo+350 {
		t.Fatalf("to balance mismatch: want %d got %d", beforeTo+350, afterTo)
	}
}

func TestSend_InsufficientFunds(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	from := createWallet(t, db, 100)
	to := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, from, to)

	beforeFrom := getBalance(t, db, from)
	beforeTo := getBalance(t, db, to)

	r := buildRouter(db)

	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":3.50}`, from, to)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d, body=%s", rr.Code, rr.Body.String())
	}

	afterFrom := getBalance(t, db, from)
	afterTo := getBalance(t, db, to)

	if afterFrom != beforeFrom || afterTo != beforeTo {
		t.Fatalf("balances changed on insufficient funds: from %d->%d, to %d->%d",
			beforeFrom, afterFrom, beforeTo, afterTo)
	}
}

func TestSend_WalletNotFound(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	to := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, to)

	missing := randHex(32)

	r := buildRouter(db)

	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":1.00}`, missing, to)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSend_SameAddress(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	addr := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, addr)

	r := buildRouter(db)

	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":1.00}`, addr, addr)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestSend_ConcurrentCrossTransfers_NoLoss(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	a := createWallet(t, db, 10000)
	b := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, a, b)

	beforeA := getBalance(t, db, a)
	beforeB := getBalance(t, db, b)

	r := buildRouter(db)

	const pairs = 6
	const amount = 1.00
	const cents = int64(100)

	var wg sync.WaitGroup
	wg.Add(pairs * 2)

	start := make(chan struct{})

	do := func(from, to string) {
		defer wg.Done()
		<-start

		body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":%.2f}`, from, to, amount)
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("concurrent send returned %d, body=%s", rr.Code, rr.Body.String())
		}
	}

	for i := 0; i < pairs; i++ {
		go do(a, b) // A->B
		go do(b, a) // B->A
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for concurrent transfers")
	}

	afterA := getBalance(t, db, a)
	afterB := getBalance(t, db, b)

	if afterA != beforeA || afterB != beforeB {
		t.Fatalf("balances drift after cross transfers: A %d->%d, B %d->%d",
			beforeA, afterA, beforeB, afterB)
	}

	if (afterA + afterB) != (beforeA + beforeB) {
		t.Fatalf("total balance changed: before=%d after=%d",
			beforeA+beforeB, afterA+afterB)
	}

	_ = cents
}

// Невалидный JSON
func TestSend_InvalidJSON(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	r := buildRouter(db)

	req := httptest.NewRequest(http.MethodPost, "/api/send", bytes.NewBufferString(`{`)) // обрезанный JSON
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid json, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSend_InvalidAddressFormat(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	r := buildRouter(db)
	body := `{"from":"abc","to":"def","amount":1.00}` // короткие адреса

	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid address, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSend_ZeroOrNegativeAmount(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	a := createWallet(t, db, 10000)
	b := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, a, b)

	r := buildRouter(db)

	for _, amt := range []string{"0", "-1.23"} {
		body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":%s}`, a, b, amt)
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400 for amount=%s, got %d body=%s", amt, rr.Code, rr.Body.String())
		}
	}
}

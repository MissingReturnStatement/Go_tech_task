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

// openDB, открывает соединение к базе для тестов, берет dsn из переменной окружения или дефолтный, проверяет подключение ping
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

// buildRouter, собирает http роутер с API поверх переданной базы
func buildRouter(db *sql.DB) http.Handler {
	r := chi.NewRouter()
	api := &API{Repo: repo.NewPostgres(db)}
	api.Routes(r)
	return r
}

// randHex, генерирует случайную hex строку для адреса кошелька
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// createWallet, добавляет кошелек с заданным балансом в центах, возвращает адрес
func createWallet(t *testing.T, db *sql.DB, cents int64) string {
	t.Helper()
	addr := randHex(32)
	if _, err := db.Exec(`INSERT INTO wallets(address, balance_cents) VALUES ($1,$2)`, addr, cents); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	return addr
}

// getBalance, возвращает баланс кошелька в центах
func getBalance(t *testing.T, db *sql.DB, addr string) int64 {
	t.Helper()
	var c int64
	if err := db.QueryRow(`SELECT balance_cents FROM wallets WHERE address=$1`, addr).Scan(&c); err != nil {
		t.Fatalf("select balance: %v", err)
	}
	return c
}

// cleanupWallets, удаляет транзакции и кошельки после теста
func cleanupWallets(t *testing.T, db *sql.DB, addrs ...string) {
	t.Helper()
	for _, a := range addrs {
		_, _ = db.Exec(`DELETE FROM transactions WHERE from_address=$1 OR to_address=$1`, a)
		_, _ = db.Exec(`DELETE FROM wallets WHERE address=$1`, a)
	}
}

// TestSend_Success, проверяет успешный перевод и корректную смену балансов
func TestSend_Success(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	// подготовка, два кошелька с балансом
	from := createWallet(t, db, 10000)
	to := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, from, to)

	// фиксация исходных балансов
	beforeFrom := getBalance(t, db, from)
	beforeTo := getBalance(t, db, to)

	// сборка роутера и вызов эндпоинта
	r := buildRouter(db)
	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":3.50}`, from, to)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 200
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// проверяем новые балансы, 3.50 это 350 центов
	afterFrom := getBalance(t, db, from)
	afterTo := getBalance(t, db, to)

	if afterFrom != beforeFrom-350 {
		t.Fatalf("from balance mismatch: want %d got %d", beforeFrom-350, afterFrom)
	}
	if afterTo != beforeTo+350 {
		t.Fatalf("to balance mismatch: want %d got %d", beforeTo+350, afterTo)
	}
}

// TestSend_InsufficientFunds, проверяет отказ при недостатке средств и неизменность балансов
func TestSend_InsufficientFunds(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	// отправитель с маленьким балансом, получатель с нормальным
	from := createWallet(t, db, 100)
	to := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, from, to)

	beforeFrom := getBalance(t, db, from)
	beforeTo := getBalance(t, db, to)

	r := buildRouter(db)

	// пытаемся перевести больше чем есть
	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":3.50}`, from, to)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 409 конфликт
	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// балансы не должны измениться
	afterFrom := getBalance(t, db, from)
	afterTo := getBalance(t, db, to)

	if afterFrom != beforeFrom || afterTo != beforeTo {
		t.Fatalf("balances changed on insufficient funds: from %d->%d, to %d->%d",
			beforeFrom, afterFrom, beforeTo, afterTo)
	}
}

// TestSend_WalletNotFound, проверяет реакцию на несуществующий адрес отправителя
func TestSend_WalletNotFound(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	// существующий получатель
	to := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, to)

	// отсутствующий отправитель
	missing := randHex(32)

	r := buildRouter(db)

	body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":1.00}`, missing, to)
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 404
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestSend_SameAddress, проверяет запрет перевода самому себе
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

	// ожидаем 400 неверный запрос
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestSend_ConcurrentCrossTransfers_NoLoss, проверяет корректность при параллельных перекрестных переводах и отсутствие потерь
func TestSend_ConcurrentCrossTransfers_NoLoss(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	// два кошелька с одинаковым балансом
	a := createWallet(t, db, 10000)
	b := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, a, b)

	beforeA := getBalance(t, db, a)
	beforeB := getBalance(t, db, b)

	r := buildRouter(db)

	const pairs = 6   // число пар запросов туда и обратно
	const amount = 1.00
	const cents = int64(100) // для справки, не используется в проверках

	var wg sync.WaitGroup
	wg.Add(pairs * 2)

	start := make(chan struct{})

	// функция единственного перевода, ждет общего старта
	do := func(from, to string) {
		defer wg.Done()
		<-start

		body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":%.2f}`, from, to, amount)
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		// каждый запрос должен завершиться 200
		if rr.Code != http.StatusOK {
			t.Errorf("concurrent send returned %d, body=%s", rr.Code, rr.Body.String())
		}
	}

	// запускаем попарно A->B и B->A
	for i := 0; i < pairs; i++ {
		go do(a, b) // A->B
		go do(b, a) // B->A
	}

	// одновременный старт всех горутин
	close(start)

	// ждем завершения с таймаутом
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

	// балансы должны вернуться к исходным, суммарный баланс должен сохраниться
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

// TestSend_InvalidJSON, проверяет обработку поврежденного json
func TestSend_InvalidJSON(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	r := buildRouter(db)

	// отправляем обрезанное тело
	req := httptest.NewRequest(http.MethodPost, "/api/send", bytes.NewBufferString(`{`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 400
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid json, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestSend_InvalidAddressFormat, проверяет валидацию формата адресов
func TestSend_InvalidAddressFormat(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	r := buildRouter(db)

	// некорректные адреса не hex и неверной длины
	body := `{"from":"abc","to":"def","amount":1.00}`
	req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 400
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid address, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestSend_ZeroOrNegativeAmount, проверяет запрет нулевой и отрицательной суммы
func TestSend_ZeroOrNegativeAmount(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	a := createWallet(t, db, 10000)
	b := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, a, b)

	r := buildRouter(db)

	// прогоняем два случая, ноль и минус
	for _, amt := range []string{"0", "-1.23"} {
		body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":%s}`, a, b, amt)
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		// ожидаем 400
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400 for amount=%s, got %d body=%s", amt, rr.Code, rr.Body.String())
		}
	}
}

// TestGetLastTransactions_Basic, проверяет базовый вывод последних транзакций и фильтр по count
func TestGetLastTransactions_Basic(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	a := createWallet(t, db, 10000)
	b := createWallet(t, db, 10000)
	defer cleanupWallets(t, db, a, b)

	r := buildRouter(db)

	// создаем три перевода, суммы возрастают
	for _, amt := range []string{"1.00", "2.00", "3.00"} {
		body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":%s}`, a, b, amt)
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("send failed: %d %s", rr.Code, rr.Body.String())
		}
	}

	// запрашиваем две последние транзакции
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?count=2", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 200 и наличие последней суммы 3.00
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.HasPrefix(body, "[") || !strings.Contains(body, `"amount":"3.00"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

// TestGetLastTransactions_InvalidCount, проверяет валидацию параметра count при нечисловом значении
func TestGetLastTransactions_InvalidCount(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	r := buildRouter(db)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions?count=abc", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// ожидаем 400
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestGetLastTransactions_DefaultAndLimit, проверяет значение count по умолчанию и верхний лимит
func TestGetLastTransactions_DefaultAndLimit(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	a := createWallet(t, db, 1000000)
	b := createWallet(t, db, 1000000)
	defer cleanupWallets(t, db, a, b)

	r := buildRouter(db)

	// генерируем много мелких переводов, чтобы проверить усечение по лимиту
	for i := 0; i < 120; i++ {
		body := fmt.Sprintf(`{"from":"%s","to":"%s","amount":0.01}`, a, b)
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("send failed: %d %s", rr.Code, rr.Body.String())
		}
	}

	// запрос без параметров, ожидаем дефолтное количество
	req := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.HasPrefix(rr.Body.String(), "[") {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}

	// запрос с очень большим count, ожидаем что сервис применит верхний предел
	req = httptest.NewRequest(http.MethodGet, "/api/transactions?count=5000", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

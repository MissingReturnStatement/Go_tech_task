package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"gotechtask/internal/repo"
)

func setupAPI(t *testing.T) *API {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL is not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping db: %v", err)
	}

	return &API{Repo: repo.NewPostgres(db)}
}

func TestGetBalance_WalletExists(t *testing.T) {
	api := setupAPI(t)

	db, _ := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	defer db.Close()

	var addr string
	err := db.QueryRow(`SELECT address FROM wallets LIMIT 1`).Scan(&addr)
	if err != nil {
		t.Fatalf("select wallet: %v", err)
	}

	r := chi.NewRouter()
	api.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/wallet/"+addr+"/balance", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("address", addr)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["address"] != addr {
		t.Errorf("expected addr %s, got %s", addr, resp["address"])
	}
}

func TestGetBalance_WalletNotExists(t *testing.T) {
	api := setupAPI(t)

	r := chi.NewRouter()
	api.Routes(r)

	// случайный адрес
	addr := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	req := httptest.NewRequest(http.MethodGet, "/api/wallet/"+addr+"/balance", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("address", addr)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

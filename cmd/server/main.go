package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	intapi  "gotechtask/internal/api"
	intdb   "gotechtask/internal/db"
	intrepo "gotechtask/internal/repo"
)

func main() {
	dsn := os.Getenv("DATABASE_URL") 
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	if addrs, err := intdb.SeedInitialWallets(db); err != nil {
		log.Fatalf("seed wallets: %v", err)
	} else if len(addrs) > 0 {
		log.Printf("seeded %d wallets (100.00 each), first=%s", len(addrs), addrs[0])
	}

	repo := intrepo.NewPostgres(db)
	api := &intapi.API{Repo: repo}

	r := chi.NewRouter()
	api.Routes(r) 
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	log.Println("server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

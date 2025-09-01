package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"strconv"

	"github.com/go-chi/chi/v5"
	"gotechtask/internal/repo"
)

type API struct {
	Repo repo.Repo
}

func (a *API) Routes(r chi.Router) {
	r.Get("/api/wallet/{address}/balance", a.getBalance)
	r.Post("/api/send", a.postSend)
	r.Get("/api/transactions", a.getLastTransactions)
}

func (a *API) getBalance(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "address")

	cents, err := a.Repo.GetBalance(r.Context(), addr)
	if err != nil {
		if err == repo.ErrWalletNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "wallet not found",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal server error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"address": addr,
		"balance": formatCents(cents),
	})
}

type sendReq struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
}

type sendResp struct {
	Status string `json:"status"`
}

func (a *API) postSend(w http.ResponseWriter, r *http.Request) {
	var req sendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if len(req.From) != 64 || len(req.To) != 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address format"})
		return
	}
	if req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be > 0"})
		return
	}

	amountCents := int64(req.Amount * 100)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	err := a.Repo.Transfer(ctx, req.From, req.To, amountCents)
	if err != nil {
		switch err {
		case repo.ErrWalletNotFound:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "wallet not found"})
		case repo.ErrInsufficientFunds:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "insufficient funds"})
		case repo.ErrSameAddress:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must differ from to"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}

	writeJSON(w, http.StatusOK, sendResp{Status: "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func formatCents(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	return sign + fmt.Sprintf("%d.%02d", c/100, c%100)
}

type txDTO struct {
	ID        int64  `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    string `json:"amount"`
	CreatedAt string `json:"created_at"`
}

func (a *API) getLastTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("count")
	n := 10
	if q != "" {
		v, err := strconv.Atoi(q)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid count"})
			return
		}
		n = v
	}
	if n <= 0 {
		n = 10
	}
	if n > 100 {
		n = 100
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	items, err := a.Repo.GetLastTransactions(ctx, n)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	out := make([]txDTO, 0, len(items))
	for _, t := range items {
		out = append(out, txDTO{
			ID:        t.ID,
			From:      t.FromAddress,
			To:        t.ToAddress,
			Amount:    formatCents(t.AmountCents),
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

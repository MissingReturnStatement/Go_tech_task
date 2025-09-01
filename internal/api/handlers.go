package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"gotechtask/internal/repo"
)

type API struct {
	Repo repo.Repo
}

func (a *API) Routes(r chi.Router) {
	r.Get("/api/wallet/{address}/balance", a.getBalance)
	r.Post("/api/send", a.postSend)
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

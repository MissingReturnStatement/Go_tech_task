package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"gotechtask/internal/repo" 
)

type API struct {
	Repo repo.Repo
}

func (a *API) Routes(r chi.Router) {
	r.Get("/api/wallet/{address}/balance", a.getBalance)
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

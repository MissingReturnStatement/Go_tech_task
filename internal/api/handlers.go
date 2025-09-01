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

// API, хранит зависимость репозитория, предоставляет обработчики http
type API struct {
	Repo repo.Repo
}

// Routes, регистрирует маршруты, баланс кошелька, перевод, последние транзакции
func (a *API) Routes(r chi.Router) {
	r.Get("/api/wallet/{address}/balance", a.getBalance)
	r.Post("/api/send", a.postSend)
	r.Get("/api/transactions", a.getLastTransactions)
}

// getBalance, берет адрес из пути, запрашивает баланс у репозитория, маппит ошибки в коды http, отдает адрес и баланс строкой
func (a *API) getBalance(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "address")

	cents, err := a.Repo.GetBalance(r.Context(), addr)
	if err != nil {
		if err == repo.ErrWalletNotFound {
			// кошелек не найден, 404
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "wallet not found",
			})
			return
		}
		// прочая ошибка, 500
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal server error",
		})
		return
	}

	// успех, возвращаем адрес и баланс в человекочитаемом виде
	writeJSON(w, http.StatusOK, map[string]string{
		"address": addr,
		"balance": formatCents(cents),
	})
}

// sendReq, входная модель перевода, адрес отправителя, адрес получателя, сумма
type sendReq struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
}

// sendResp, выходная модель перевода, статус выполнения
type sendResp struct {
	Status string `json:"status"`
}

// postSend, валидирует тело запроса, проверяет формат адресов и сумму, конвертирует в центы, вызывает перевод у репозитория с таймаутом, возвращает коды в зависимости от ошибки
func (a *API) postSend(w http.ResponseWriter, r *http.Request) {
	var req sendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// битый json, 400
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if len(req.From) != 64 || len(req.To) != 64 {
		// неверная длина адресов, 400
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address format"})
		return
	}
	if req.Amount <= 0 {
		// сумма должна быть больше нуля, 400
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be > 0"})
		return
	}

	// переводим сумму в центы, без округления вверх, дробная часть отбрасывается правилами float к int64
	amountCents := int64(req.Amount * 100)

	// ограничиваем время операции перевода, чтобы не зависать
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// выполняем перевод через доменную логику репозитория
	err := a.Repo.Transfer(ctx, req.From, req.To, amountCents)
	if err != nil {
		// маппим доменные ошибки в http коды
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

	// успех, отдаем ок
	writeJSON(w, http.StatusOK, sendResp{Status: "ok"})
}

// writeJSON, устанавливает заголовок контента, пишет код ответа, кодирует структуру в json
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// formatCents, форматирует сумму в центах в строку с двумя десятичными знаками, учитывает знак
func formatCents(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	return sign + fmt.Sprintf("%d.%02d", c/100, c%100)
}

// txDTO, представление транзакции для ответа, id, адреса, сумма строкой, время создания
type txDTO struct {
	ID        int64  `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    string `json:"amount"`
	CreatedAt string `json:"created_at"`
}

// getLastTransactions, читает параметр count, применяет дефолт и верхний предел, запрашивает последние транзакции у репозитория, форматирует ответ
func (a *API) getLastTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("count")
	n := 10
	if q != "" {
		v, err := strconv.Atoi(q)
		if err != nil {
			// неверный count, 400
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid count"})
			return
		}
		n = v
	}
	// нормализация границ, минимум десять по умолчанию, максимум сто
	if n <= 0 {
		n = 10
	}
	if n > 100 {
		n = 100
	}

	// короткий таймаут для простого запроса чтения
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	items, err := a.Repo.GetLastTransactions(ctx, n)
	if err != nil {
		// внутренняя ошибка, 500
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// маппим доменную модель в dto, форматируем сумму и время в rfc3339
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
	// успешный ответ со списком
	writeJSON(w, http.StatusOK, out)
}

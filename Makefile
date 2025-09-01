# Makefile

PROJECT_NAME=go_tech_task
COMPOSE=docker compose

.PHONY: up down reset build logs db-psql test balance send getlast

# Запуск всего проекта (db + migrate + app)
up:
	$(COMPOSE) up -d --build

# Остановка контейнеров (данные сохраняются)
down:
	$(COMPOSE) down

# Полный сброс с удалением volume (чистая БД)
reset:
	$(COMPOSE) down -v

# Пересобрать контейнер приложения
build:
	$(COMPOSE) build app

# Логи приложения
logs:
	$(COMPOSE) logs -f app

# Консоль psql внутри контейнера
db-psql:
	docker exec -it wallet-db psql -U app -d wallet_service

# Прогнать тесты
test:
	DATABASE_URL=postgres://app:app@127.0.0.1:5433/wallet_service?sslmode=disable \
		go test -v ./...

# Демонстрация API (просто тестовые штуки, чтобы показать/проверить что работает)

# Проверить баланс первого кошелька
balance:
	@ADDR=$$(docker exec wallet-db psql -U app -d wallet_service -t -A -c "SELECT address FROM wallets LIMIT 1;" | tr -d '\r\n'); \
	echo "ADDR=$$ADDR"; \
	curl -s "http://localhost:8080/api/wallet/$$ADDR/balance" | jq

# Отправить 1.23 от первого ко второму
send:
	@FROM=$$(docker exec wallet-db psql -U app -d wallet_service -t -A -c "SELECT address FROM wallets LIMIT 1;" | tr -d '\r\n'); \
	TO=$$(docker exec wallet-db psql -U app -d wallet_service -t -A -c "SELECT address FROM wallets OFFSET 1 LIMIT 1;" | tr -d '\r\n'); \
	curl -s -X POST http://localhost:8080/api/send \
	  -H 'Content-Type: application/json' \
	  -d "$$(printf '{"from":"%s","to":"%s","amount":1.23}' $$FROM $$TO)" | jq


# Получить последние 5 транзакций
getlast:
	curl -s "http://localhost:8080/api/transactions?count=5" | jq

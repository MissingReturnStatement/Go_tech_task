# Go Tech Task — Wallet Service

Простой сервис кошельков на Go с PostgreSQL, переводы между адресами и журнал транзакций. При старте сидируются кошельки со стартовым балансом.

## Быстрый старт

### 1. Предустановки
- Docker и Docker Compose
- Make для удобных команд

### 2. Создайте `.env` в корне
```
POSTGRES_USER=app
POSTGRES_PASSWORD=app
POSTGRES_DB=wallet_service
POSTGRES_PORT=5432

DATABASE_URL=postgres://app:app@db:5432/wallet_service?sslmode=disable
```

### 3. Запуск через Docker Compose (но лучше использовать Make)
```bash
docker compose up --build
```

Приложение поднимется на `http://localhost:8080`. 
В логах будет видно сидирование кошельков:
```
seeded 10 wallets (100.00 each), first=<address>
```

## Эндпоинты

### Healthcheck
```bash
curl -s http://localhost:8080/health
# ok
```

### Баланс кошелька
```bash
curl -s http://localhost:8080/api/wallet/<address>/balance
# {"address":"<address>","balance":"100.00"}
```

### Перевод между кошельками
```bash
curl -s -X POST http://localhost:8080/api/send \
  -H "Content-Type: application/json" \
  -d '{"from":"<from_addr>","to":"<to_addr>","amount":3.50}'
# {"status":"ok"}
```

Коды ошибок: 
400 invalid json, invalid address format, amount must be > 0, from must differ from to 
404 wallet not found 
409 insufficient funds 
500 internal error

### Последние транзакции
```bash
curl -s "http://localhost:8080/api/transactions?count=5"
# [{"id":..., "from":"...","to":"...","amount":"3.00","created_at":"..."}]
```
`count` по умолчанию 10, максимум 100.

## Makefile: основные команды

```bash
# запустить проект (db + migrate + app)
make up

# остановить контейнеры (данные сохраняются)
make down

# полный сброс с удалением volume (чистая БД)
make reset

# пересобрать контейнер приложения
make build

# смотреть логи приложения
make logs

# зайти в psql внутри контейнера
make db-psql

# прогнать тесты (локальный postgres на 5433)
make test

# быстрые проверки API

# баланс первого кошелька
make balance

# отправить 1.23 от первого ко второму
make send

# последние 5 транзакций
make getlast
```

## Доступ к БД

```bash
docker compose exec db psql -U app -d wallet_service
```

Примеры:
```sql
SELECT address, balance_cents FROM wallets LIMIT 5;
SELECT * FROM transactions ORDER BY created_at DESC LIMIT 10;
```
## Что происходит при старте

- приложение читает `DATABASE_URL` 
- подключается к PostgreSQL и пингует его 
- сидирует `N=10` кошельков по `100.00`, если таблица пуста 
- поднимает сервер на `:8080`
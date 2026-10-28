Заводим базенку

```sh
createdb -h localhost -U postgres gophermart
```

Запускаем: 

```sh
go build -o gophermart ./cmd/gophermart

./gophermart \
  -a localhost:8080 \
  -d 'postgres://postgres:postgres@localhost:5432/gophermart?sslmode=disable' \
  -r http://localhost:8081
```

| Флаг | Переменная окружения | Дефолт |
| --- | --- | --- |
| `-a` | `RUN_ADDRESS` | `localhost:8080` |
| `-d` | `DATABASE_URI` | REQUIRED |
| `-r` | `ACCRUAL_SYSTEM_ADDRESS` | `http://localhost:8081` |

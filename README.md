# Online cinema analytics

События фильмов идут из продюсера в Kafka, ClickHouse хранит сырой поток, отдельный сервис считает дневные метрики в PostgreSQL и выгружает день в MinIO. Grafana читает уже посчитанные агрегаты.

Стек: Go, OpenAPI, Avro, Kafka, Schema Registry, ClickHouse, PostgreSQL, Flyway, Grafana, MinIO.

## Запуск

```bash
docker compose up --build
```

Одна команда поднимает кластер и применяет миграции. Продюсер слушает `http://localhost:8090`, агрегация — `http://localhost:8091`.

Проверка:

```bash
curl -sS http://localhost:8090/health
curl -sS http://localhost:8091/health
curl -sS -X POST http://localhost:8090/events \
  -H 'content-type: application/json' \
  -d '{"user_id":"user-e2e","movie_id":"movie-01","event_type":"LIKED","timestamp":"2026-04-01T15:04:05Z","device_type":"DESKTOP","session_id":"sess-1"}'
```

Дашборд: `http://localhost:3000`. Логин выключен, дашборд открывается сразу.

Остановка с удалением данных:

```bash
docker compose down -v
```

E2E ходит в уже поднятый compose:

```bash
python3 scripts/e2e.py
```

Повторный прогон удаляет свои строки в ClickHouse по `event_id`. Кодогенерация OpenAPI, её же выполняет сборка образа:

```bash
make generate
```

`services/producer/internal/api/openapi.gen.go` и `services/aggregation/internal/api/openapi.gen.go` лежат в `.gitignore`.

## Порты

| Сервис | Порт хоста |
| --- | --- |
| producer | 8090 |
| aggregation | 8091 |
| PostgreSQL | 5434 |
| ClickHouse HTTP | 8123 |
| Kafka | 9092, 9093, 9094 |
| Schema Registry | 8085 |
| Grafana | 3000 |
| MinIO API и консоль | 9100 и 9101 |

PostgreSQL: `analytics` / `analytics`, база `analytics`. ClickHouse HTTP: пользователь `default`, пароль `analytics`, база `analytics`. MinIO: `analytics` / `analyticssecret`, бакет `movie-analytics`.

Образы: `confluentinc/cp-kafka:8.0.9`, `confluentinc/cp-schema-registry:8.0.9`, `clickhouse/clickhouse-server:25.8.33`, `grafana/grafana:12.3.11`, `alpine/minio:RELEASE.2025-10-15T17-29-55Z`. Официальный `minio/minio` с Docker Hub снят, тег Alpine — тот же релиз MinIO.

## Ключ партиции

Ключ сообщения — `user_id`. Топик `movie-events` создан с 3 партициями, `replication.factor=3`, `min.insync.replicas=2`. Продюсер пишет с `acks=all`.

Kafka хранит порядок внутри партиции. В сессии просмотра порядок один: `VIEW_STARTED`, затем пауза и продолжение, затем `VIEW_FINISHED`, и `progress_seconds` растёт. У сессии один `user_id`, поэтому ключ `user_id` кладёт её в одну партицию вместе с поиском этого пользователя. DAU и retention тоже считаются по пользователю. Сами метрики ClickHouse считает по `timestamp`, на их числа ключ не влияет. События популярного фильма при этом распределяются по пользователям.

Повтор публикации: 5 попыток, первая пауза 100 мс, множитель 2, потолок 2 с. Успешная публикация пишет в лог `event_id`, `event_type` и `timestamp`.

## Схема события

Файл `services/producer/api/avro/movie_event.avsc`, subject `movie-events-value`, compatibility `BACKWARD`, первая версия — 1. `event_id` — UUID, его генерирует продюсер. `timestamp` — `timestamp-millis` в UTC. `progress_seconds` обязателен у `VIEW_*` и отсутствует у `LIKED` и `SEARCHED`. `movie_id` непустой у всех типов, включая поиск.

Генератор включён в compose и детерминирован. На старте он публикует 8 календарных дней UTC, включая текущий, затем раз в минуту проверяет, не наступил ли новый день. Пользователи 1–8 начинают в самый старый день и по одному отваливаются, 9–14 приходят на третий день окна, 15–20 — на шестой. `movie-01` и `movie-02` забирают большую часть стартов, остальные делят хвост. Часть сессий обрывается до `VIEW_FINISHED`, у завершённых разная длина просмотра. Из-за этого DAU, топ и когорты на дашборде не константа.

## Хранение и метрики

ClickHouse читает топик движком Kafka (`AvroConfluent`, группа `clickhouse-movie-events`) и materialized view складывает строки в `movie_events`. Партиция — календарный день UTC, сортировка `(event_date, user_id, timestamp, event_id)`.

Агрегация не читает Kafka. Раз в минуту (`AGGREGATION_INTERVAL`) она пересчитывает текущий день UTC: заново собирает `user_day`, считает DAU (`uniqExact`), среднее время (`avgIf` по `VIEW_FINISHED`), топ-10 фильмов по числу `VIEW_STARTED` и конверсию `VIEW_FINISHED / VIEW_STARTED`. Retention D1 и D7 пишется для когорты этого дня, предыдущего дня и дня семидневной давности. Когорта — дата первого `VIEW_STARTED`, возврат — любое событие в этот день жизни. В PostgreSQL доля хранится от 0 до 1. Нет пользователей в когорте — `NULL`. Нет завершённых просмотров или стартов — среднее и конверсия `NULL`. Нет событий за день — DAU равен 0.

При старте сервис один раз пересчитывает последние 8 дней UTC, от старого к новому, дождавшись сырых событий генератора. Ручной пересчёт: `POST /recalculate?date=YYYY-MM-DD`.

В логе старта есть дата. Завершение цикла:

```text
cycle finished, processed N records in M sec
```

N — число сырых событий этой даты. Повторный расчёт обновляет строку с тем же ключом `(metric_date, metric_name, movie_id, cohort_day)`, а не добавляет дубль. Запись в PostgreSQL повторяется до 5 раз.

Те же точки лежат в ClickHouse `metric_points` (`ReplacingMergeTree`). Панели DAU, конверсии, топа и среднего времени читают её с `FINAL`. Пустое поле «Top day» показывает день с наибольшим числом стартов, а не сегодняшний: в последний день окна пользователей меньше, и топ был бы почти плоским. Когортная таблица читает `user_day`: когорта — `min(event_date)` среди дней с `view_started > 0`, Day 0…Day 7 — доля пользователей когорты со строкой активности в этот день. День, который ещё не наступил, остаётся пустым, а не 0%.

## Экспорт

Раз в сутки и сразу после стартового пересчёта предыдущий день UTC уходит в `s3://movie-analytics/daily/YYYY-MM-DD/aggregates.json`. Ручной запуск: `POST /export?date=YYYY-MM-DD`. Повтор за ту же дату перезаписывает объект. Если PostgreSQL или MinIO недоступны, ошибка пишется в лог, дата остаётся в памяти процесса, и следующий плановый запуск повторяет её, а затем выгружает предыдущий день, если это уже другая дата.

## Ошибки

Тело ошибки: `error_code`, `message`, для валидации ещё `details.fields`. Невалидное событие — 400 `VALIDATION_ERROR`. Kafka не приняла сообщение после повторов, сбой записи метрик или выгрузки — 503 `UNAVAILABLE`.

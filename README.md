# Online cinema analytics

Аналитика онлайн-кинотеатра: события просмотров идут через Kafka в ClickHouse, отдельный сервис раз в минуту считает дневные метрики, Grafana их показывает, а раз в сутки день выгружается в S3.

```text
генератор и POST /events
          │
          v
      producer ──Avro──> Kafka, 3 брокера ──> ClickHouse movie_events
                                                       │
                                           aggregation, раз в минуту
                                     ┌─────────────────┼──────────────────┐
                                     v                 v                  v
                  ClickHouse user_day, metric_points   PostgreSQL   MinIO, раз в сутки
                                     │
                                     v
                                  Grafana
```

Стек: Go, OpenAPI, Avro, Kafka, Schema Registry, ClickHouse, PostgreSQL, Flyway, Grafana, MinIO.

## Быстрый старт

Нужен Docker с Compose. Для тестов ещё Go 1.27 и Python 3.

```bash
docker compose up -d --build
docker compose ps
```

Через 1–2 минуты все сервисы `healthy`. Init-контейнеры (`kafka-init`, `clickhouse-init`, `flyway`, `minio-init`) завершаются с кодом 0, их видно в `docker compose ps -a`. Затем генератор около 40 секунд догружает историю за неделю, и агрегатор её досчитывает.

Что открыть:

| Что | Адрес | Доступ |
| --- | --- | --- |
| Дашборд Grafana | http://localhost:3000 | без логина |
| Консоль MinIO | http://localhost:9101 | `analytics` / `analyticssecret` |
| Producer API | http://localhost:8090 | |
| Aggregation API | http://localhost:8091 | |
| Schema Registry | http://localhost:8085 | |

Остановить и удалить все данные:

```bash
docker compose down -v
```

После изменения SQL-схем в `db/` старые volume надо удалить именно так. Init создаёт таблицы через `IF NOT EXISTS` и старые не пересоздаёт.

## Дашборд

http://localhost:3000 сразу открывает «Movie analytics». Он обновляется раз в 30 секунд, агрегатор пересчитывает сегодняшний день раз в минуту.

- Retention Cohort Heatmap: строка — когорта (день первого просмотра), Day 0…Day 7 — доля когорты, вернувшейся в этот день. Ещё не наступившие дни пустые.
- DAU, View conversion (`VIEW_FINISHED / VIEW_STARTED`), Average watch time — по дням.
- Top movies — топ-10 по числу стартов за день из поля «Top day» (формат `YYYY-MM-DD`). Если поле пустое — за последний завершённый день диапазона.

Все панели фильтруются диапазоном времени в правом верхнем углу. Время везде UTC.

## Попробовать руками

### Отправить событие

Событие с текущим временем:

```bash
curl -sS -i -X POST http://localhost:8090/events \
  -H 'content-type: application/json' \
  -d "{\"user_id\":\"user-manual\",\"movie_id\":\"movie-03\",\"event_type\":\"VIEW_STARTED\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"device_type\":\"TV\",\"session_id\":\"sess-manual\",\"progress_seconds\":0}"
```

Ответ `201` с `{"event_id":"..."}`. Типы: `VIEW_STARTED`, `VIEW_PAUSED`, `VIEW_RESUMED`, `VIEW_FINISHED` (с `progress_seconds`), `LIKED`, `SEARCHED` (без него). Устройства: `MOBILE`, `DESKTOP`, `TV`, `TABLET`. `timestamp` можно передать с любым смещением, хранится он в UTC.

Невалидное событие возвращает `400` со списком полей:

```bash
curl -sS -X POST http://localhost:8090/events \
  -H 'content-type: application/json' \
  -d '{"user_id":"","movie_id":"movie-03","event_type":"LIKED","timestamp":"2026-11-25T10:00:00Z","device_type":"TV","session_id":"s"}'
# {"details":{"fields":[{"field":"user_id","violation":"minimum string length is 1"}]},"error_code":"VALIDATION_ERROR","message":"validation failed"}
```

Через несколько секунд событие лежит в ClickHouse, а через минуту оно учтено в DAU за сегодня:

```bash
docker compose exec clickhouse clickhouse-client --password analytics -d analytics \
  -q "SELECT * FROM movie_events WHERE user_id = 'user-manual' FORMAT Vertical"
```

### Пересчитать и выгрузить день

```bash
YESTERDAY=$(date -u -v-1d +%F 2>/dev/null || date -u -d yesterday +%F)
curl -sS -X POST "http://localhost:8091/recalculate?date=$YESTERDAY"   # {"date":"...","status":"ok"}
curl -sS -X POST "http://localhost:8091/export?date=$YESTERDAY"        # {"date":"...","status":"ok"}
curl -sS http://localhost:8090/health http://localhost:8091/health
```

Пересчёт и выгрузку можно повторять: строки в PostgreSQL обновляются, файл в S3 перезаписывается. Неверная дата даёт `400`, недоступность PostgreSQL, ClickHouse или MinIO — `503`.

### Логи

```bash
docker compose logs -f producer aggregation                    # всё подряд
docker compose logs producer | grep 'generator published history'
docker compose logs aggregation | grep -E 'cycle finished|export finished'
docker compose logs aggregation | grep -E 'WARN|ERROR'
docker compose logs clickhouse | grep -i kafka                 # проблемы чтения топика
```

Producer пишет строку на каждое опубликованное событие (`event_id`, `event_type`, `timestamp`). Aggregation пишет начало и конец каждого пересчёта (`cycle finished, processed N records in M sec`), выгрузки (`export finished date=... rows=...`), повторы и ошибки.

### ClickHouse

Интерактивная консоль:

```bash
docker compose exec clickhouse clickhouse-client --password analytics -d analytics
```

Полезные запросы. Сырые таблицы читайте с `FINAL`, иначе будут видны ещё не схлопнутые копии событий:

```sql
-- последние события
SELECT timestamp, event_type, user_id, movie_id, progress_seconds FROM movie_events ORDER BY timestamp DESC LIMIT 10;
-- объём по дням
SELECT event_date, count() FROM movie_events FINAL GROUP BY event_date ORDER BY event_date;
-- события сегодня по типам
SELECT event_type, count() FROM movie_events FINAL WHERE event_date = today() GROUP BY event_type;
-- метрики, которые видит Grafana
SELECT metric_date, metric_name, movie_id, cohort_day, value FROM metric_points FINAL
WHERE metric_date = yesterday() ORDER BY metric_name, movie_id, cohort_day;
```

Таблицы: `movie_events_kafka` (движок Kafka), `movie_events_mv` (перекладывает в `movie_events`), `movie_events`, `user_day` (активность пользователя за день), `user_day_staging`, `metric_points`. HTTP-интерфейс тоже доступен:

```bash
curl -sS 'http://localhost:8123/?user=default&password=analytics' --data-binary 'SELECT count() FROM analytics.movie_events FINAL'
```

### PostgreSQL

```bash
docker compose exec postgres psql -U analytics -d analytics
```

```sql
-- все метрики за вчера
SELECT metric_date, metric_name, movie_id, cohort_day, round(value::numeric, 3) AS value
FROM metric_points WHERE metric_date = current_date - 1 ORDER BY metric_name, movie_id, cohort_day;
-- DAU по дням
SELECT metric_date, value FROM metric_points WHERE metric_name = 'dau' ORDER BY metric_date;
-- retention: metric_date — день когорты, cohort_day — 1 или 7, NULL — день ещё не наступил
SELECT metric_date AS cohort, cohort_day, round(value::numeric, 3) FROM metric_points
WHERE metric_name = 'retention' ORDER BY 1, 2;
```

Таблица одна, `metric_points`, ключ `(metric_date, metric_name, movie_id, cohort_day)`. Пустой `movie_id` и `cohort_day = -1` означают «не относится к метрике». Миграции Flyway лежат в `db/postgres/migration`.

### S3 (MinIO)

Проще всего смотреть в консоли http://localhost:9101, бакет `movie-analytics`, папка `daily/`. Из терминала хватит `curl` 7.75 и новее:

```bash
curl -sS --user analytics:analyticssecret --aws-sigv4 'aws:amz:us-east-1:s3' \
  'http://localhost:9100/movie-analytics?list-type=2&prefix=daily/'
curl -sS --user analytics:analyticssecret --aws-sigv4 'aws:amz:us-east-1:s3' \
  "http://localhost:9100/movie-analytics/daily/$YESTERDAY/aggregates.json"
```

### Kafka и Schema Registry

```bash
# топик: 3 партиции, RF 3, min ISR 2
docker compose exec kafka-1 kafka-topics --bootstrap-server localhost:29092 --describe --topic movie-events
# отставание ClickHouse от топика
docker compose exec kafka-1 kafka-consumer-groups --bootstrap-server localhost:29092 --describe --group clickhouse-movie-events
# прочитать сообщения в JSON (ключ — user_id)
docker compose exec schema-registry kafka-avro-console-consumer --bootstrap-server kafka-1:29092 \
  --topic movie-events --from-beginning --max-messages 5 \
  --property schema.registry.url=http://localhost:8081 --property print.key=true \
  --key-deserializer org.apache.kafka.common.serialization.StringDeserializer 2>/dev/null | grep event_id
# схема и режим совместимости
curl -sS http://localhost:8085/subjects/movie-events-value/versions/latest
curl -sS http://localhost:8085/config/movie-events-value
```

### Сломать что-нибудь

Остановить один брокер не страшно: `min.insync.replicas=2`, и producer продолжает отвечать `201`. Без двух брокеров producer примерно через 1,5 секунды повторов отвечает `503`:

```bash
docker compose stop kafka-2 kafka-3
# POST /events -> 503 UNAVAILABLE
docker compose start kafka-2 kafka-3
```

Без PostgreSQL пересчёт и выгрузка отвечают `503`, без MinIO — выгрузка. В логе aggregation видны повторы и ошибка. Упавший пересчёт сам повторяется на следующем тике, в течение минуты. Упавшая выгрузка повторяется при следующей плановой выгрузке, раньше её можно запустить руками:

```bash
docker compose stop postgres
curl -sS -X POST "http://localhost:8091/recalculate?date=$YESTERDAY"   # 503 через несколько секунд повторов
docker compose start postgres
docker compose logs --since 2m aggregation

docker compose stop minio
curl -sS -X POST "http://localhost:8091/export?date=$YESTERDAY"        # 503
docker compose start minio
```

## Тесты

```bash
make test       # gofmt и модульные тесты обоих сервисов, без инфраструктуры
make e2e        # нужен поднятый compose: событие через producer -> Kafka -> ClickHouse, метрики, S3, Grafana
```

E2E можно запускать сколько угодно раз, свои строки в ClickHouse он удаляет по `event_id`. Модульные тесты гоняет и сборка образов.

Остальные команды `make`: `up` (`docker compose up --build` в foreground), `down` (с удалением volume), `generate` (кодогенерация OpenAPI; `internal/api/openapi.gen.go` обоих сервисов лежат в `.gitignore`, сборка образа генерирует их сама), `fmt`.

## Порты и доступы

| Сервис | Порт хоста | Доступ |
| --- | --- | --- |
| producer | 8090 | |
| aggregation | 8091 | |
| PostgreSQL | 5434 | `analytics` / `analytics`, база `analytics` |
| ClickHouse HTTP | 8123 | `default` / `analytics`, база `analytics` |
| Kafka | 9092, 9093, 9094 | |
| Schema Registry | 8085 | |
| Grafana | 3000 | без логина |
| MinIO API и консоль | 9100 и 9101 | `analytics` / `analyticssecret`, бакет `movie-analytics` |

Образы: `confluentinc/cp-kafka:8.0.9`, `confluentinc/cp-schema-registry:8.0.9`, `clickhouse/clickhouse-server:25.8.33`, `grafana/grafana:12.3.11`, `alpine/minio:RELEASE.2025-10-15T17-29-55Z`. Официальный `minio/minio` с Docker Hub снят, тег Alpine — тот же релиз MinIO.

## Как это устроено

### Ключ партиции

Ключ сообщения — `user_id`. Топик `movie-events` создан с 3 партициями, `replication.factor=3`, `min.insync.replicas=2`. Продюсер пишет с `acks=all`.

Kafka хранит порядок внутри партиции. В сессии просмотра порядок один: `VIEW_STARTED`, затем пауза и продолжение, затем `VIEW_FINISHED`, и `progress_seconds` растёт. У сессии один `user_id`, поэтому ключ `user_id` кладёт её в одну партицию вместе с поиском этого пользователя. DAU и retention тоже считаются по пользователю. Сами метрики ClickHouse считает по `timestamp`, на их числа ключ не влияет. События популярного фильма при этом распределяются по пользователям.

Повтор публикации: 5 попыток, первая пауза 100 мс, множитель 2, потолок 2 с. Успешная публикация пишет в лог `event_id`, `event_type` и `timestamp`.

### Схема события

Файл `services/producer/api/avro/movie_event.avsc`, subject `movie-events-value`, compatibility `BACKWARD`, первая версия — 1. `event_id` — UUID, его генерирует продюсер. `timestamp` — `timestamp-millis` в UTC. `progress_seconds` обязателен у `VIEW_*` и отсутствует у `LIKED` и `SEARCHED`. `movie_id` непустой у всех типов, включая поиск.

### Генератор

Генератор включён в compose и работает потоком в реальном времени: каждые 5 секунд он публикует события, чьё время уже наступило. На старте это догоняет историю с полуночи UTC семь дней назад, дальше идёт около 7 событий в минуту (примерно 10 тысяч в сутки, DAU около 1200). Событий из будущего нет. Историю (около 50 тысяч событий) публикуют 32 воркера (около 40 секунд), события одного пользователя всегда идут через один воркер, поэтому их порядок в партиции сохраняется.

Расписание — чистая функция календарной даты. Каждый день приходит когорта из 300–400 новых пользователей (`user-YYYYMMDD-NNN`). В день жизни N пользователь возвращается с вероятностью 55%, 42%, … 20% для N = 1…7, затем пропадает. У активного пользователя 1–3 сессии, разнесённые по суткам. Последняя сессия может начаться до самой полуночи и обрывается на ней, поэтому поток не затихает поздно вечером по UTC. Сессия: иногда `SEARCHED`, затем `VIEW_STARTED`, `VIEW_PAUSED`; примерно 60% сессий продолжаются `VIEW_RESUMED` и `VIEW_FINISHED`, часть заканчивается `LIKED`. `progress_seconds` растёт со скоростью воспроизведения. Фильмы выбираются с весами 10:9:…:1, поэтому `movie-01` лидирует.

`event_id` — UUIDv5 от сессии и номера шага. Рестарт продюсера публикует ту же историю с теми же id, повтор публикации после таймаута Kafka — тот же id, и ClickHouse схлопывает копии (см. ниже). Если Kafka недоступна, генератор не двигает курсор и повторяет пачку на следующем тике.

### Хранение и метрики

ClickHouse читает топик движком Kafka (`AvroConfluent`, группа `clickhouse-movie-events`) и materialized view складывает строки в `movie_events`. Таблица — `ReplacingMergeTree`, партиция — календарный день UTC, сортировка `(event_date, user_id, timestamp, event_id)`. Копии одного события совпадают по ключу сортировки, поэтому агрегация читает сырые события с `FINAL` и не видит дублей доставки.

Агрегация не читает Kafka. Раз в минуту (`AGGREGATION_INTERVAL`) она сравнивает число сырых строк по дням за последнюю неделю с прошлым тиком и пересчитывает текущий день UTC, каждый день, где число изменилось (поздние события, догоняющая история), и каждый день, чей пересчёт упал раньше. Пересчёт дня собирает `user_day` во вспомогательной таблице и подменяет партицию через `REPLACE PARTITION`, поэтому дашборд не видит пустого дня. Затем считаются DAU (`uniqExact`), среднее время (`avgIf` по `VIEW_FINISHED`), топ-10 фильмов по числу `VIEW_STARTED` и конверсия `VIEW_FINISHED / VIEW_STARTED`.

Retention D1 и D7 считается по `user_day` для когорты этого дня, предыдущего дня и дня семидневной давности. Когорта — дата первого `VIEW_STARTED`, возврат — любая активность в этот день жизни. В PostgreSQL доля хранится от 0 до 1. День жизни, который ещё не наступил, хранится как `NULL`, а не 0. Когорта без пользователей строк не получает. Нет завершённых просмотров или стартов — среднее и конверсия `NULL`. Нет событий за день — DAU равен 0.

При старте сервис достраивает `user_day` для старых дат, которых там нет, и сразу выполняет тик. Пока история генератора ещё доезжает, следующие тики пересчитывают изменившиеся дни. Ручной пересчёт: `POST /recalculate?date=YYYY-MM-DD`, он доводится до конца, даже если клиент отключился.

В логе старта есть дата. Завершение цикла:

```text
cycle finished, processed N records in M sec
```

N — число сырых событий этой даты. Повторный расчёт обновляет строку с тем же ключом `(metric_date, metric_name, movie_id, cohort_day)`, а не добавляет дубль. Строки, которые пересчёт больше не выдаёт (фильм выпал из топа, когорта опустела), удаляются в той же транзакции. Запись в PostgreSQL повторяется до 5 раз. Если пересчёт всё же упал, дата остаётся в памяти процесса и повторяется следующим тиком.

Те же точки лежат в ClickHouse `metric_points` (`ReplacingMergeTree(computed_at, is_deleted)`). Устаревшие ключи получают в том же INSERT строку с `is_deleted = 1`, и `FINAL` их скрывает. Все панели читают агрегаты с `FINAL` и фильтруются выбранным диапазоном времени. Пустое поле «Top day» показывает последний завершённый день диапазона. Когортная таблица читает `user_day`: когорта — `min(event_date)` среди дней с `view_started > 0`, Day 0…Day 7 — доля пользователей когорты со строкой активности в этот день, ячейки окрашены от красного к зелёному. День, который ещё не наступил, остаётся пустым, а не 0%.

### Экспорт

Раз в сутки (`EXPORT_INTERVAL`) и сразу после старта в `s3://movie-analytics/daily/YYYY-MM-DD/aggregates.json` уходят вчерашний день, позавчерашний (у его когорты стал окончательным D1) и день восьмидневной давности (окончательным стал D7). Если тик пересчитал прошлый день, затронутые даты выгружаются заново сразу. Ручной запуск: `POST /export?date=YYYY-MM-DD`. Повтор за ту же дату перезаписывает объект, успешная выгрузка пишется в лог. Если PostgreSQL или MinIO недоступны, ошибка пишется в лог, дата остаётся в памяти процесса, и следующий плановый запуск повторяет её.

### Ошибки

Тело ошибки: `error_code`, `message`, для валидации ещё `details.fields` с именем поля и причиной. Невалидное событие или дата — 400 `VALIDATION_ERROR`. Kafka не приняла сообщение после повторов, сбой записи метрик или выгрузки — 503 `UNAVAILABLE`.

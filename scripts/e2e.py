#!/usr/bin/env python3
"""e2e against an already running compose stack."""

import hashlib
import hmac
import json
import os
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PRODUCER = os.environ.get("PRODUCER", "http://localhost:8090")
AGGREGATION = os.environ.get("AGGREGATION", "http://localhost:8091")
CLICKHOUSE = os.environ.get("CLICKHOUSE", "http://localhost:8123")
SCHEMA = os.environ.get("SCHEMA_REGISTRY", "http://localhost:8085")
GRAFANA = os.environ.get("GRAFANA", "http://localhost:3000")
S3 = os.environ.get("S3", "http://localhost:9100")
S3_ACCESS = os.environ.get("S3_ACCESS_KEY", "analytics")
S3_SECRET = os.environ.get("S3_SECRET_KEY", "analyticssecret")
S3_BUCKET = os.environ.get("S3_BUCKET", "movie-analytics")


class ApiError(Exception):
    pass


def request(base, method, path, body=None, expected=200):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        base + path,
        data=data,
        headers={"Content-Type": "application/json"},
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            raw = response.read()
            status = response.status
            payload = json.loads(raw) if raw else None
    except urllib.error.HTTPError as err:
        raw = err.read()
        status = err.code
        payload = json.loads(raw) if raw else None
    if status != expected:
        raise ApiError(f"{method} {path} -> {status}, want {expected}: {payload}")
    return payload


def get_json(url):
    with urllib.request.urlopen(url, timeout=15) as response:
        return json.loads(response.read())


def ch(sql):
    req = urllib.request.Request(
        CLICKHOUSE + "/?user=default&password=analytics",
        data=sql.encode(),
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            raw = response.read()
    except urllib.error.HTTPError as err:
        raise SystemExit(err.read().decode()) from err
    if "FORMAT JSON" in sql:
        return json.loads(raw)
    return raw.decode()


def wait_health():
    deadline = time.time() + 180
    last = None
    while time.time() < deadline:
        try:
            producer = request(PRODUCER, "GET", "/health")
            aggregation = request(AGGREGATION, "GET", "/health")
            ch("SELECT 1")
            if producer.get("status") == "ok" and aggregation.get("status") == "ok":
                return
        except Exception as err:  # noqa: BLE001
            last = err
        time.sleep(1)
    raise SystemExit(f"services did not become ready: {last}")


def e2e_ids():
    body = ch(
        "SELECT toString(event_id) AS event_id "
        "FROM analytics.movie_events WHERE startsWith(user_id, 'e2e-') FORMAT JSON"
    )
    return [row["event_id"] for row in body["data"]]


def delete_ids(ids):
    if not ids:
        return
    for event_id in ids:
        uuid.UUID(event_id)
    listed = ", ".join(f"toUUID('{event_id}')" for event_id in ids)
    ch(
        "DELETE FROM analytics.movie_events "
        f"WHERE event_id IN ({listed}) SETTINGS mutations_sync = 2"
    )


def wait_row(event_id):
    deadline = time.time() + 45
    while time.time() < deadline:
        body = ch(
            "SELECT toString(event_id) AS id, user_id, movie_id, event_type, "
            "toUnixTimestamp64Milli(timestamp) AS ts, device_type, session_id, progress_seconds "
            "FROM analytics.movie_events "
            f"WHERE event_id = toUUID('{event_id}') FORMAT JSON"
        )
        if body["data"]:
            return body["data"][0]
        time.sleep(1)
    raise SystemExit(f"event {event_id} did not appear in ClickHouse")


def absent(user_id):
    deadline = time.time() + 8
    while time.time() < deadline:
        body = ch(
            "SELECT toUInt64(count()) AS c FROM analytics.movie_events "
            f"WHERE user_id = '{user_id}' FORMAT JSON"
        )
        if int(body["data"][0]["c"]) != 0:
            raise SystemExit(f"rejected event landed in ClickHouse: {user_id}")
        time.sleep(1)


def event(user_id, event_type, progress):
    body = {
        "user_id": user_id,
        "movie_id": "movie-e2e",
        "event_type": event_type,
        "timestamp": "2026-04-01T15:04:05Z",
        "device_type": "DESKTOP",
        "session_id": "sess-e2e",
    }
    if progress is not None:
        body["progress_seconds"] = progress
    return body


def check_pipeline():
    delete_ids(e2e_ids())

    versions = get_json(SCHEMA + "/subjects/movie-events-value/versions")
    if versions != [1]:
        raise SystemExit(f"schema versions: {versions}")
    compat = get_json(SCHEMA + "/config/movie-events-value")
    if compat.get("compatibilityLevel") != "BACKWARD":
        raise SystemExit(f"compatibility: {compat}")

    described = subprocess.run(
        [
            "docker", "compose", "exec", "-T", "kafka-1",
            "kafka-topics", "--bootstrap-server", "kafka-1:29092",
            "--describe", "--topic", "movie-events",
        ],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if described.returncode != 0:
        raise SystemExit(described.stderr.strip() or described.stdout)
    text = described.stdout
    if "PartitionCount: 3" not in text or "ReplicationFactor: 3" not in text or "min.insync.replicas=2" not in text:
        raise SystemExit(text)

    view_user = "e2e-" + uuid.uuid4().hex
    liked_user = "e2e-" + uuid.uuid4().hex
    view = request(PRODUCER, "POST", "/events", event(view_user, "VIEW_STARTED", 0), expected=201)
    liked = request(PRODUCER, "POST", "/events", event(liked_user, "LIKED", None), expected=201)
    for item in (view, liked):
        uuid.UUID(item["event_id"])

    view_row = wait_row(view["event_id"])
    liked_row = wait_row(liked["event_id"])
    expect = int(datetime(2026, 4, 1, 15, 4, 5, tzinfo=timezone.utc).timestamp() * 1000)
    for row, user_id, event_type, progress in (
        (view_row, view_user, "VIEW_STARTED", 0),
        (liked_row, liked_user, "LIKED", None),
    ):
        if row["user_id"] != user_id or row["movie_id"] != "movie-e2e" or row["event_type"] != event_type:
            raise SystemExit(row)
        if row["device_type"] != "DESKTOP" or row["session_id"] != "sess-e2e":
            raise SystemExit(row)
        if int(row["ts"]) != expect:
            raise SystemExit(row)
        if progress is None and row["progress_seconds"] is not None:
            raise SystemExit(row)
        if progress is not None and row["progress_seconds"] != progress:
            raise SystemExit(row)

    missing_user = "e2e-" + uuid.uuid4().hex
    missing = request(
        PRODUCER,
        "POST",
        "/events",
        {
            "user_id": missing_user,
            "event_type": "VIEW_STARTED",
            "timestamp": "2026-04-01T15:04:05Z",
            "device_type": "TV",
            "session_id": "sess-e2e",
            "progress_seconds": 1,
        },
        expected=400,
    )
    if missing["error_code"] != "VALIDATION_ERROR":
        raise SystemExit(missing)
    liked_progress_user = "e2e-" + uuid.uuid4().hex
    liked_progress = request(
        PRODUCER,
        "POST",
        "/events",
        event(liked_progress_user, "LIKED", 10),
        expected=400,
    )
    if liked_progress["error_code"] != "VALIDATION_ERROR":
        raise SystemExit(liked_progress)
    absent(missing_user)
    absent(liked_progress_user)
    delete_ids([view["event_id"], liked["event_id"]])


def psql(sql):
    result = subprocess.run(
        ["docker", "compose", "exec", "-T", "postgres", "psql", "-U", "analytics", "-d", "analytics", "-tA", "-c", sql],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise SystemExit(result.stderr.strip() or "psql failed")
    return result.stdout.strip()


def wait_dau(day):
    deadline = time.time() + 180
    while time.time() < deadline:
        count = psql(
            "SELECT count(*) FROM metric_points "
            f"WHERE metric_date = DATE '{day}' AND metric_name = 'dau'"
        )
        if count == "1":
            return
        time.sleep(2)
    raise SystemExit(f"no DAU for {day}")


def sign_get(path):
    now = datetime.now(timezone.utc)
    amzdate = now.strftime("%Y%m%dT%H%M%SZ")
    datestamp = now.strftime("%Y%m%d")
    payload = hashlib.sha256(b"").hexdigest()
    host = S3.removeprefix("http://").removeprefix("https://")
    canonical_headers = f"host:{host}\nx-amz-content-sha256:{payload}\nx-amz-date:{amzdate}\n"
    signed_headers = "host;x-amz-content-sha256;x-amz-date"
    # MinIO joins canonical headers that already end with a newline, so a blank line
    # stays between the headers and the signed-header list.
    canonical = "GET\n" + path + "\n\n" + canonical_headers + "\n" + signed_headers + "\n" + payload
    scope = f"{datestamp}/us-east-1/s3/aws4_request"
    string_to_sign = "AWS4-HMAC-SHA256\n" + amzdate + "\n" + scope + "\n" + hashlib.sha256(canonical.encode()).hexdigest()

    def sign(key, message):
        return hmac.new(key, message.encode(), hashlib.sha256).digest()

    signing = sign(sign(sign(sign(("AWS4" + S3_SECRET).encode(), datestamp), "us-east-1"), "s3"), "aws4_request")
    signature = hmac.new(signing, string_to_sign.encode(), hashlib.sha256).hexdigest()
    authorization = (
        f"AWS4-HMAC-SHA256 Credential={S3_ACCESS}/{scope}, "
        f"SignedHeaders={signed_headers}, Signature={signature}"
    )
    req = urllib.request.Request(
        S3 + path,
        headers={
            "Host": host,
            "x-amz-date": amzdate,
            "x-amz-content-sha256": payload,
            "Authorization": authorization,
        },
        method="GET",
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as err:
        raise SystemExit(err.read().decode()) from err


def check_metrics():
    yesterday = (datetime.now(timezone.utc) - timedelta(days=1)).date().isoformat()
    wait_dau(yesterday)
    request(AGGREGATION, "POST", f"/recalculate?date={yesterday}")
    if psql(
        "SELECT count(*) FROM metric_points "
        f"WHERE metric_date = DATE '{yesterday}' AND metric_name = 'dau'"
    ) != "1":
        raise SystemExit("recalculate duplicated the DAU row")
    bad = request(AGGREGATION, "POST", "/recalculate?date=2026-02-31", expected=400)
    if bad["error_code"] != "VALIDATION_ERROR":
        raise SystemExit(bad)
    request(AGGREGATION, "POST", f"/export?date={yesterday}")
    rows = sign_get(f"/{S3_BUCKET}/daily/{yesterday}/aggregates.json")
    names = {row["metric_name"] for row in rows}
    if not {"dau", "conversion", "avg_watch_seconds", "retention"} <= names:
        raise SystemExit(names)
    request(AGGREGATION, "POST", f"/export?date={yesterday}")
    again = sign_get(f"/{S3_BUCKET}/daily/{yesterday}/aggregates.json")
    if [row["metric_name"] for row in again] != [row["metric_name"] for row in rows]:
        raise SystemExit("a second export changed the metric list")


def check_grafana():
    health = get_json(GRAFANA + "/api/health")
    if health.get("database") != "ok":
        raise SystemExit(health)
    search = get_json(GRAFANA + "/api/search?query=Movie")
    titles = [item.get("title") for item in search]
    if "Movie analytics" not in titles:
        raise SystemExit(titles)


def main():
    wait_health()
    check_pipeline()
    check_metrics()
    check_grafana()
    print("e2e ok")


if __name__ == "__main__":
    main()

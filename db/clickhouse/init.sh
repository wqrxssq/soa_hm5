#!/bin/sh
set -eu

i=0
until clickhouse-client --host clickhouse --user default --password analytics --query "SELECT 1" >/dev/null 2>/tmp/ch.err; do
  i=$((i + 1))
  if [ "$i" -gt 30 ]; then
    echo "clickhouse is not ready" >&2
    cat /tmp/ch.err >&2 || true
    exit 1
  fi
  sleep 1
done

for f in /sql/*.sql; do
  echo "apply $f"
  clickhouse-client --host clickhouse --user default --password analytics --queries-file "$f"
done

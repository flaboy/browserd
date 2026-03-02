#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/e2e/docker-compose.yml"

cleanup() {
  docker compose -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker compose -f "$COMPOSE_FILE" up -d --build

for i in $(seq 1 60); do
  minio_init_id="$(docker compose -f "$COMPOSE_FILE" ps -aq minio-init)"
  if [[ -n "${minio_init_id}" ]]; then
    minio_init_status="$(docker inspect -f '{{.State.Status}}' "$minio_init_id")"
    if [[ "$minio_init_status" == "exited" ]]; then
      minio_init_exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$minio_init_id")"
      if [[ "$minio_init_exit_code" == "0" ]]; then
        break
      fi
      echo "minio-init exited with code $minio_init_exit_code"
      docker compose -f "$COMPOSE_FILE" logs minio-init
      exit 1
    fi
  fi
  sleep 1
  if [[ "$i" == "60" ]]; then
    echo "minio-init timeout"
    docker compose -f "$COMPOSE_FILE" logs minio-init
    exit 1
  fi
done

for i in $(seq 1 60); do
  if curl -fsS http://localhost:17011/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 1
  if [[ "$i" == "60" ]]; then
    echo "browserd health check timeout"
    exit 1
  fi
done

cd "$ROOT_DIR"
BROWSERD_BASE_URL="http://localhost:17011" go test ./e2e -run TestBrowserdMinIOSmoke -v

echo "e2e smoke passed"

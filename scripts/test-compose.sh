#!/bin/sh

set -eu

project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$project_dir"

./scripts/init-env.sh >/dev/null
set -a
. ./.env
set +a

export CAMPUS_HTTP_PORT="${CAMPUS_TEST_HTTP_PORT:-18080}"
export CAMPUS_MYSQL_PORT="${CAMPUS_TEST_MYSQL_PORT:-23306}"
export CAMPUS_REDIS_PORT="${CAMPUS_TEST_REDIS_PORT:-26379}"
export CAMPUS_INTEGRATION_BASE_URL="http://127.0.0.1:${CAMPUS_HTTP_PORT}"
export CAMPUS_INTEGRATION_REDIS_ADDRESS="127.0.0.1:${CAMPUS_REDIS_PORT}"
export CAMPUS_INTEGRATION_MYSQL_ADMIN_DSN="root:${CAMPUS_MYSQL_ROOT_PASSWORD}@tcp(127.0.0.1:${CAMPUS_MYSQL_PORT})/?charset=utf8mb4&parseTime=true&loc=Local"
export CAMPUS_INTEGRATION_MYSQL_DSN="${CAMPUS_MYSQL_USER}:${CAMPUS_MYSQL_PASSWORD}@tcp(127.0.0.1:${CAMPUS_MYSQL_PORT})/${CAMPUS_MYSQL_DATABASE}?charset=utf8mb4&parseTime=true&loc=Local"
export CAMPUS_INTEGRATION_RUN_ID="$(date +%s)"

cleanup() {
	docker compose down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

cleanup
docker compose up -d --build

wait_ready() {
	attempt=0
	until curl --fail --silent --show-error "${CAMPUS_INTEGRATION_BASE_URL}/health/ready" >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 60 ]; then
			docker compose ps
			return 1
		fi
		sleep 1
	done
}

wait_ready
CAMPUS_INTEGRATION_PERSISTENCE_PHASE=seed go test -count=1 -tags=integration ./tests/integration

docker compose restart api
wait_ready
CAMPUS_INTEGRATION_PERSISTENCE_PHASE=verify go test -count=1 -tags=integration -run '^TestComposePersistenceVerify$' ./tests/integration

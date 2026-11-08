#!/usr/bin/env bash
# Starts the Compose stack from an image and checks that a run executes in
# the runner container. Usage: PENDOLO_IMAGE=pendolo:ci deploy/smoke-test.sh
set -euo pipefail

: "${PENDOLO_IMAGE:?set PENDOLO_IMAGE to the image to test}"
export PENDOLO_IMAGE
cd "$(dirname "$0")"

cleanup() {
  docker compose logs --no-color || true
  docker compose down -v || true
  rm -rf automations secrets pendolo.yaml
}
trap cleanup EXIT

mkdir -p automations/hello secrets
openssl rand -hex 24 > secrets/runner-token
cat > pendolo.yaml <<'YAML'
runner:
  url: http://runner:8090
  token: secret://runner-token
YAML
cat > automations/hello/automation.yaml <<'YAML'
apiVersion: pendolo/v1
kind: Automation
metadata: { name: hello }
triggers: [ { manual: {} } ]
steps:
  - id: where
    type: shell
    run: 'echo "ran on $(cat /etc/hostname)"'
YAML
chmod -R a+rX automations secrets pendolo.yaml

docker compose up -d --no-build
for _ in $(seq 1 30); do
  if docker compose exec -T pendolo pendolo healthcheck >/dev/null 2>&1; then break; fi
  sleep 1
done
docker compose exec -T pendolo pendolo healthcheck

docker compose exec -T pendolo pendolo trigger hello
for _ in $(seq 1 30); do
  status=$(docker compose exec -T pendolo pendolo runs --json | jq -r '.[0].status')
  [ "$status" = succeeded ] || [ "$status" = failed ] && break
  sleep 1
done
run=$(docker compose exec -T pendolo pendolo runs --json | jq -r '.[0].id')
docker compose exec -T pendolo pendolo show "$run"
[ "$status" = succeeded ] || { echo "run $run: $status" >&2; exit 1; }

runner_host=$(docker compose exec -T runner cat /etc/hostname | tr -d '\r')
if ! docker compose exec -T pendolo pendolo show --json "$run" | jq -r '.steps[0].logs' | grep -q "ran on $runner_host"; then
  echo "the script did not run in the runner container ($runner_host)" >&2
  exit 1
fi
echo "smoke test passed: the run executed in the runner container"

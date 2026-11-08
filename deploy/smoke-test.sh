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
openssl rand -base64 32 > secrets/pendolo-master-key
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
chmod a+w automations # the server writes activated drafts here

docker compose up -d --no-build
for _ in $(seq 1 30); do
  if docker compose exec -T pendolo pendolo healthcheck >/dev/null 2>&1; then break; fi
  sleep 1
done
docker compose exec -T pendolo pendolo healthcheck

# The server can write automations (builder activations); the runner cannot.
docker compose exec -T pendolo sh -c 'touch /automations/.write-test && rm /automations/.write-test'
if docker compose exec -T runner sh -c 'touch /automations/.write-test' 2>/dev/null; then
  echo "the runner can write to the automations folder" >&2
  exit 1
fi

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
echo "the run executed in the runner container"

# Dashboard and API: create an owner, sign in, read the run back.
printf 'smoke-password-123\n' | docker compose exec -T pendolo pendolo users create --email smoke@example.com --owner
curl -fsS http://127.0.0.1:8080/ | grep -q '<div id="root"' || { echo "dashboard not served" >&2; exit 1; }
jar=$(mktemp)
curl -fsS -c "$jar" -H 'Content-Type: application/json' -H 'X-Pendolo-Request: 1' \
  -d '{"email":"smoke@example.com","password":"smoke-password-123"}' http://127.0.0.1:8080/api/v1/auth/login >/dev/null
api_status=$(curl -fsS -b "$jar" "http://127.0.0.1:8080/api/v1/workspaces/default/runs/$run" | jq -r .run.status)
rm -f "$jar"
[ "$api_status" = succeeded ] || { echo "API returned status $api_status" >&2; exit 1; }
echo "smoke test passed: runner, dashboard and API work"

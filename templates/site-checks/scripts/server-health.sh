#!/usr/bin/env bash
# Checks a Linux server over SSH: disk usage, failed services, pending updates.
# Environment:
#   SSH_TARGET          user@host
#   SSH_PRIVATE_KEY     private key contents (injected from a secret)
#   DISK_LIMIT_PERCENT  fail when a filesystem is fuller than this (default 85)
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET is required}"
: "${SSH_PRIVATE_KEY:?SSH_PRIVATE_KEY is required}"
limit="${DISK_LIMIT_PERCENT:-85}"

key_file=$(mktemp)
trap 'rm -f "$key_file"' EXIT
printf '%s\n' "$SSH_PRIVATE_KEY" > "$key_file"
chmod 600 "$key_file"

remote() {
  ssh -i "$key_file" -o BatchMode=yes -o ConnectTimeout=10 \
    -o StrictHostKeyChecking=accept-new "$SSH_TARGET" "$@"
}

status=0

echo "== disk"
full=$(remote "df -P -x tmpfs -x devtmpfs | awk 'NR>1 {gsub(\"%\",\"\",\$5); if (\$5 > $limit) print \$6\" \"\$5\"%\"}'")
if [ -n "$full" ]; then
  echo "filesystems over ${limit}%: $full" >&2
  status=1
else
  echo "all filesystems under ${limit}%"
fi

echo "== services"
failed=$(remote "systemctl --failed --no-legend --plain 2>/dev/null | awk '{print \$1}'" || true)
if [ -n "$failed" ]; then
  echo "failed services: $failed" >&2
  status=1
else
  echo "no failed services"
fi

echo "== updates"
remote "if command -v apt >/dev/null; then apt list --upgradable 2>/dev/null | tail -n +2 | wc -l; \
  elif command -v dnf >/dev/null; then dnf -q check-update | grep -c . || true; else echo unknown; fi" \
  | sed 's/^/pending updates: /'

exit "$status"

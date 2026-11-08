#!/usr/bin/env bash
# Fails when the TLS certificate of a host expires within N days.
# Usage: tls-expiry.sh <host> [days, default 21]
set -euo pipefail

host="${1:?usage: tls-expiry.sh <host> [days]}"
days="${2:-21}"

end_date=$(echo | openssl s_client -servername "$host" -connect "$host:443" 2>/dev/null \
  | openssl x509 -noout -enddate | cut -d= -f2)

if [ -z "$end_date" ]; then
  echo "could not read the certificate of $host" >&2
  exit 1
fi

echo "certificate of $host expires on $end_date"

if ! echo | openssl s_client -servername "$host" -connect "$host:443" 2>/dev/null \
  | openssl x509 -noout -checkend $((days * 86400)) >/dev/null; then
  echo "certificate expires within $days days" >&2
  exit 1
fi

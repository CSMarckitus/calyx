#!/usr/bin/env bash
# Checks the dependency rule of the client package: no gRPC or core names in src/ and
# examples/, and no Composer package in "require" except php and ext-*.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
client=$(dirname "$here")/client

# Fixed strings, so the backslashes need no escape. PHP names ignore letter case, so grep
# does too. vendor/ is not scanned.
for dir in "$client/src" "$client/examples"; do
  [ -d "$dir" ] || continue
  # grep exits with 0 for a match, 1 for no match, and 2 for an error (an error must fail too).
  status=0
  grep -rniF -e 'Google\' -e 'Grpc\' -e 'Calyx\Core\V1' -e 'Calyx\Client\Core' "$dir" || status=$?
  if [ "$status" -eq 0 ]; then
    echo "deps:check failed: a file uses a gRPC or core name." >&2
    exit 1
  fi
  if [ "$status" -ne 1 ]; then
    echo "deps:check failed: grep could not read $dir." >&2
    exit 1
  fi
done

"$here/run.sh" deps-composer

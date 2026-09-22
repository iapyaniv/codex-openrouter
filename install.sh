#!/bin/sh
set -eu

if ! command -v node >/dev/null 2>&1; then
  echo 'Install Node.js 22+ and npm, then rerun this installer.' >&2
  exit 1
fi

exec node "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/install.mjs" "$@"

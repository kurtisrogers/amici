#!/usr/bin/env bash
#
# Build and start Amici for the browser suite.
#
# The database lives in a fixed temporary directory that is wiped on every
# start rather than a fresh mktemp directory each time. Both give a clean
# slate, but this one does not need a cleanup trap: the last line of this
# script is an exec, so the shell is gone and any trap would never run. A
# mktemp per start would quietly leave a directory behind on every run.
#
# Everything below is explicit rather than relying on a default, so that
# reading this file tells you exactly what the tests are running against.
set -euo pipefail

cd "$(dirname "$0")/.."
repo_root="$(cd .. && pwd)"

port="${AMICI_E2E_PORT:-8081}"
work_dir="${TMPDIR:-/tmp}/amici-e2e"
binary="${work_dir}/amici"

rm -rf "${work_dir}"
mkdir -p "${work_dir}"

echo "amici-e2e: building" >&2
(cd "${repo_root}" && go build -o "${binary}" ./cmd/amici)

echo "amici-e2e: starting on 127.0.0.1:${port}" >&2

# AMICI_ENV=test is what allows fixtures at all; production refuses them
# outright and will not start with the flag set. The secret is fixed so that
# an invite code minted before a restart still verifies afterwards.
AMICI_ENV=test \
AMICI_ADDR="127.0.0.1:${port}" \
AMICI_BASE_URL="http://127.0.0.1:${port}" \
AMICI_DB="${work_dir}/amici-e2e.db" \
AMICI_SECRET_KEY="e2e-secret-e2e-secret-e2e-secret" \
AMICI_ENABLE_FIXTURES=true \
AMICI_SECURE_COOKIES=false \
  exec "${binary}"

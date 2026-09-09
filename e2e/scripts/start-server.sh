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

# Built with the fixtures tag, because the endpoints this suite uses to reset
# the world and read the outbox only exist in a binary built that way. A
# release build does not contain them at all, which is the point of the tag.
# CI builds and vets the untagged configuration separately, so the release
# artifact cannot break unnoticed just because nothing here exercises it.
echo "amici-e2e: building" >&2
(cd "${repo_root}" && go build -tags fixtures -o "${binary}" ./cmd/amici)

echo "amici-e2e: starting on 127.0.0.1:${port}" >&2

# AMICI_ENV=test is the second of the three locks on the fixture endpoints:
# the build tag above is the first, this flag is the third, and production
# refuses the flag outright. The secret is fixed so that an invite code minted
# before a restart still verifies afterwards.
AMICI_ENV=test \
AMICI_ADDR="127.0.0.1:${port}" \
AMICI_BASE_URL="http://127.0.0.1:${port}" \
AMICI_DB="${work_dir}/amici-e2e.db" \
AMICI_SECRET_KEY="e2e-secret-e2e-secret-e2e-secret" \
AMICI_ENABLE_FIXTURES=true \
AMICI_SECURE_COOKIES=false \
  exec "${binary}"

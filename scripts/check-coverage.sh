#!/usr/bin/env bash
set -euo pipefail

profile=$(mktemp)
trap 'rm -f "$profile"' EXIT
if [[ -z "${FEATURE_FLAGS_POSTGRES_DSN:-}" || -z "${FEATURE_FLAGS_VALKEY_ADDRESS:-}" ]]; then
	echo 'coverage requires PostgreSQL and Valkey integration environment variables' >&2
	exit 1
fi
packages=$(go list ./... | grep -v '/featureflagstest$' | grep -v '/memory$')
cover_packages=$(printf '%s\n' "$packages" | paste -sd, -)
# shellcheck disable=SC2086
go test -tags=integration -covermode=atomic -coverpkg="$cover_packages" \
	-coverprofile="$profile" $packages
coverage=$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%", "", $3); print $3}')
printf 'meaningful production statement coverage: %s%%\n' "$coverage"
if [[ "$coverage" != "100.0" ]]; then
	echo 'meaningful production coverage must remain 100.0%' >&2
	exit 1
fi

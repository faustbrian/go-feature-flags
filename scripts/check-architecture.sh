#!/usr/bin/env bash
set -euo pipefail

production=$(mktemp)
trap 'rm -f "$production"' EXIT
rg --files -g '*.go' -g '!**/*_test.go' >"$production"

if xargs rg -n '"(reflect|unsafe)"|//go:linkname|import[[:space:]]+"C"|func[[:space:]]+init[[:space:]]*\(' <"$production"; then
	echo 'forbidden production runtime mechanism found' >&2
	exit 1
fi

dependencies=$(go list -deps -f '{{.ImportPath}}' ./...)
if printf '%s\n' "$dependencies" | grep -E '^github\.com/faustbrian/go-(authorization|config|rule-engine|settings)(/|$)'; then
	echo 'owning sibling package entered the dependency graph' >&2
	exit 1
fi

CGO_ENABLED=0 go test ./...
echo 'architecture constraints pass'

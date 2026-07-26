#!/usr/bin/env bash
set -euo pipefail

duration=${1:-2s}
go test . -run '^$' -fuzz '^FuzzImportNeverPanics$' -fuzztime="$duration"
go test . -run '^$' -fuzz '^FuzzBucketIsStableAndBounded$' -fuzztime="$duration"
go test . -run '^$' -fuzz '^FuzzDefinitionValidationNeverPanics$' -fuzztime="$duration"
go test . -run '^$' -fuzz '^FuzzContextEvaluationNeverPanics$' -fuzztime="$duration"

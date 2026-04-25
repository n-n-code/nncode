#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

note() {
    printf '==> %s\n' "$*"
}

run_cmd() {
    note "$*"
    "$@"
}

run_cmd bash scripts/check-release-hygiene.sh
run_cmd go vet ./...

if [[ -n "$golangci_lint" ]]; then
    run_cmd "$golangci_lint" fmt ./...
fi

golangci_lint=""
if command -v golangci-lint >/dev/null 2>&1; then
    golangci_lint="golangci-lint"
elif [[ -x "$(go env GOPATH)/bin/golangci-lint" ]]; then
    golangci_lint="$(go env GOPATH)/bin/golangci-lint"
fi
if [[ -n "$golangci_lint" ]]; then
    run_cmd "$golangci_lint" run
else
    note "Skipping golangci-lint because it is not installed (see https://golangci-lint.run/docs/welcome/install/local/)"
fi

run_cmd go test ./...
run_cmd go build -o nncode ./cmd/nncode/

note "Smoke-checking ./nncode -h"
run_cmd ./nncode -h >/dev/null

cat <<'EOF'

Automated release checks passed.

Manual review items still remaining:
  - Review README.md and AGENTS.md for release accuracy.
  - Run `./nncode -check` (and optionally `./nncode doctor -live`) on the target host.
  - Exercise the agent loop against a reachable model if the change touches it.
EOF

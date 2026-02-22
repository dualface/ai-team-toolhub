#!/usr/bin/env bash
set -euo pipefail

echo "Checking MCP docs generation drift..."
go -C toolhub run ./cmd/mcpdocgen > docs/mcp-tools.generated.md.tmp
if ! diff -q docs/mcp-tools.generated.md docs/mcp-tools.generated.md.tmp > /dev/null 2>&1; then
    echo "DRIFT DETECTED: docs/mcp-tools.generated.md is out of date"
    diff docs/mcp-tools.generated.md docs/mcp-tools.generated.md.tmp || true
    rm -f docs/mcp-tools.generated.md.tmp
    exit 1
fi
rm -f docs/mcp-tools.generated.md.tmp
echo "MCP docs generation: OK"

echo "Running doc drift Go tests..."
go -C toolhub test -run 'TestDocDrift' -count=1 ./internal/core/
echo "Doc drift checks: ALL PASSED"

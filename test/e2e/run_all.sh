#!/bin/bash
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
#
# Runs every enterprise e2e test: each directory here with a run.sh. It covers the
# enterprise gears only. The engine's own regression lives in the engine repository.

set -u

BASE_DIR="$(cd "$(dirname "$0")" && pwd)"

CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

FAILED=()
DIRS=()
for dir in "${BASE_DIR}"/*/; do
    dir=${dir%/}
    [ "$(basename "$dir")" = "common" ] && continue
    [ -f "$dir/run.sh" ] && DIRS+=("$dir")
done

echo -e "${CYAN}==============================================================================${NC}"
echo -e "${CYAN}fluxrig-ent e2e: ${#DIRS[@]} tests${NC}"
echo -e "${CYAN}==============================================================================${NC}"

n=0
for dir in "${DIRS[@]}"; do
    n=$((n + 1))
    name=$(basename "$dir")
    echo ""
    echo -e "${CYAN}>>> [${n}/${#DIRS[@]}] ${name}${NC}"
    if bash "$dir/run.sh"; then
        echo -e "${GREEN}[PASS]${NC} ${name}"
    else
        echo -e "${RED}[FAIL]${NC} ${name}"
        FAILED+=("$name")
    fi
done

echo ""
if [ ${#FAILED[@]} -eq 0 ]; then
    echo -e "${GREEN}[PASS] all ${#DIRS[@]} enterprise e2e tests passed${NC}"
    exit 0
fi
echo -e "${RED}[FAIL] ${#FAILED[@]} of ${#DIRS[@]} enterprise e2e tests failed:${NC}"
for t in "${FAILED[@]}"; do echo "  - ${t}"; done
exit 1

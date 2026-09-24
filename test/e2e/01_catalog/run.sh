#!/bin/bash
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
#
# The Mixer serves the gear manifest catalog. The enterprise Mixer lists the
# enterprise gears next to the engine's own, and the open-source Mixer lists none
# of them. That difference is the reason fluxrig-mixer-ent exists.
set -uo pipefail

BASE_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${BASE_DIR}/../common/ent_e2e.sh"
trap stop_ent EXIT

banner "Enterprise gear catalog E2E"
FAILS=0

gear_types() {
    curl -s "http://127.0.0.1:${API_PORT}/api/v1/gears"
}

# --- Enterprise Mixer ---
section "Enterprise Mixer"
prepare_ent_workspace catalog "$BASE_DIR" 9150 4263 rack-catalog
start_ent_mixer
catalog=$(gear_types)

for gear in sim_source sim_responder io_tcp codec_iso8583; do
    if echo "$catalog" | grep -q "\"${gear}\""; then
        log_success "The enterprise Mixer lists ${gear}."
    else
        log_error "The enterprise Mixer does not list ${gear}."
        FAILS=$((FAILS + 1))
    fi
done

for gear in sim_source sim_responder; do
    code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${API_PORT}/api/v1/gears/${gear}")
    if [ "$code" = "200" ]; then
        log_success "The enterprise Mixer serves the ${gear} manifest."
    else
        log_error "GET /gears/${gear} answered HTTP ${code}."
        FAILS=$((FAILS + 1))
    fi
done
stop_ent

# --- Open-source Mixer ---
section "Open-source Mixer"
OSS_MIXER="${FLUXRIG_DIR}/bin/fluxrig-mixer"
if [ ! -x "$OSS_MIXER" ]; then
    fail "The open-source Mixer is not built at ${OSS_MIXER}. make test-e2e builds it."
fi
prepare_ent_workspace catalog_oss "$BASE_DIR" 9150 4263 rack-catalog
start_ent_mixer "$OSS_MIXER"
catalog=$(gear_types)

if echo "$catalog" | grep -q '"io_tcp"'; then
    log_success "The open-source Mixer lists the engine's own gears."
else
    log_error "The open-source Mixer answered no catalog."
    FAILS=$((FAILS + 1))
fi
for gear in sim_source sim_responder; do
    if echo "$catalog" | grep -q "\"${gear}\""; then
        log_error "The open-source Mixer lists ${gear}."
        FAILS=$((FAILS + 1))
    else
        log_success "The open-source Mixer does not list ${gear}."
    fi
done

if [ "$FAILS" -eq 0 ]; then
    banner "ENTERPRISE GEAR CATALOG E2E PASSED"
    exit 0
fi
banner "ENTERPRISE GEAR CATALOG E2E FAILED ($FAILS checks)"
exit 1

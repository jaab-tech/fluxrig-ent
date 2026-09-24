#!/bin/bash
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
#
# sim_source on a real Rack: a scenario deployed through the enterprise Mixer makes
# the Rack generate ISO 8583 traffic from the spec, fields-only, and the encoder next
# to it packs every message into wire bytes. The check is on the path, not on the
# values: the Robot suites check the values.
set -uo pipefail

BASE_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${BASE_DIR}/../common/ent_e2e.sh"
trap stop_ent EXIT

RACK_NAME="rack-sim-source"

banner "sim_source E2E"

prepare_ent_workspace sim_source "$BASE_DIR" 9151 4264 "$RACK_NAME"
section "Starting the enterprise Mixer and Rack"
start_ent_mixer
start_ent_rack "$RACK_NAME"

section "Deploying the scenario"
import_scenario "${BASE_DIR}/scenario.yaml"
sleep 6

section "Verification"
FAILS=0
RACK_LOGS="${WORK_DIR}/rack/rack.stdout ${WORK_DIR}/rack/logs/rack.log"

# The Rack writes each log line to its stdout and to its log file: count one copy.
encoded=$(grep -c "direction=encode.*mti=0100" "${WORK_DIR}/rack/rack.stdout" 2>/dev/null)
log_info "Encode log lines for 0100 requests: ${encoded}"
if [ "$encoded" -ge 3 ]; then
    log_success "sim_source generated traffic and the encoder packed it (${encoded} encode log lines in 6s at 20 tps)."
else
    log_error "The encoder logged ${encoded} encode lines: sim_source did not generate."
    FAILS=$((FAILS + 1))
fi

# shellcheck disable=SC2086
if cat $RACK_LOGS 2>/dev/null | grep -qiE "unknown gear type|panic:"; then
    log_error "The Rack logged an unknown gear type or a panic."
    FAILS=$((FAILS + 1))
else
    log_success "No unknown gear type and no panic in the Rack log."
fi

if [ "$FAILS" -eq 0 ]; then
    banner "SIM_SOURCE E2E PASSED"
    exit 0
fi
banner "SIM_SOURCE E2E FAILED ($FAILS checks)"
exit 1

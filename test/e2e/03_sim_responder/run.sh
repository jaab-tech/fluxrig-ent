#!/bin/bash
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
#
# sim_responder on a real Rack: a terminal sends ISO 8583 requests over a real socket
# to an io -> codec -> sim_responder -> codec -> io pipeline, and gets the paired
# response back. The check is on the path and on MTI pairing: the Robot suites check
# what the rules do to each response.
set -uo pipefail

BASE_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${BASE_DIR}/../common/ent_e2e.sh"
trap stop_ent EXIT

RACK_NAME="rack-sim-responder"
RESPONDER_PORT=18591

# Requests in the wire format the codec reads: the MTI, then a bitmap of 32 ASCII hex
# characters (the primary bitmap and a zeroed secondary slot), then the data elements.
# The io gateway reads a binary bitmap instead, so the two disagree, and this test
# talks to the codec.
#   0800 carries no data element.
#   0100 carries the PAN (2), the amount (4), the STAN (11) and the terminal id (41).
BITMAP_EMPTY="00000000000000000000000000000000"
BITMAP_2_4_11_41="50200000008000000000000000000000"
REQ_0800="0800${BITMAP_EMPTY}"
REQ_0100="0100${BITMAP_2_4_11_41}""16""4111111111111111""000000010000""000001""TERM0001"

banner "sim_responder E2E"

# The exchange script runs in the project's Python environment, not whichever
# python3 the PATH names first.
setup_python_env ""

prepare_ent_workspace sim_responder "$BASE_DIR" 9152 4265 "$RACK_NAME"
section "Starting the enterprise Mixer and Rack"
start_ent_mixer
start_ent_rack "$RACK_NAME"

section "Deploying the scenario"
import_scenario "${BASE_DIR}/scenario.yaml"
wait_for_port "$RESPONDER_PORT" 20 || fail "The responder pipeline did not bind :${RESPONDER_PORT}"
sleep 2

section "Terminal -> sim_responder"
FAILS=0

# Usage: expect_reply $DESCRIPTION $REQUEST $EXPECTED_MTI
expect_reply() {
    local description="$1" request="$2" want="$3" reply mti
    reply=$(python3 "${BASE_DIR}/exchange.py" "$RESPONDER_PORT" "$request" 2>"${WORK_DIR}/exchange.err")
    if [ -z "$reply" ]; then
        log_error "${description}: no reply ($(cat "${WORK_DIR}/exchange.err"))."
        FAILS=$((FAILS + 1))
        return
    fi
    mti="${reply:0:4}"
    if [ "$mti" = "$want" ]; then
        log_success "${description}: the responder answered ${mti}."
    else
        log_error "${description}: expected ${want}, got ${mti}."
        FAILS=$((FAILS + 1))
    fi
}

expect_reply "Network management request" "$REQ_0800" 0810
expect_reply "Authorization request" "$REQ_0100" 0110

section "Verification"
if grep -qE "direction=encode.*mti=0110" "${WORK_DIR}/rack/rack.stdout" 2>/dev/null; then
    log_success "The encoder packed the response the responder authored."
else
    log_error "No encoded 0110 in the Rack log."
    FAILS=$((FAILS + 1))
fi

if [ "$FAILS" -eq 0 ]; then
    banner "SIM_RESPONDER E2E PASSED"
    exit 0
fi
banner "SIM_RESPONDER E2E FAILED ($FAILS checks)"
exit 1

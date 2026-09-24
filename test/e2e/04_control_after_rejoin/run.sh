#!/bin/bash
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
#
# A Rack starts its saved scenario while the Mixer is away, and the Mixer joins it later
# without the gears being started again. The gears that were already running must take
# the Mixer's control commands: sim_source, set to on_control, generates nothing until a
# sim.start reaches it, and stops when a sim.stop does. Every wire is inside the Rack, so
# nothing depends on the bus but the commands themselves.
set -uo pipefail

BASE_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${BASE_DIR}/../common/ent_e2e.sh"
trap stop_ent EXIT

RACK_NAME="rack-sim-control"
GEAR="traffic"
RACK_STDOUT_NAME="rack/rack.stdout"

ENROLL_TIMEOUT=30
RESUME_TIMEOUT=30
REJOIN_TIMEOUT=60
COMMAND_TIMEOUT=20
# Messages the decoder must read after sim.start. At 20 tps this is a fraction of a second.
MIN_DECODED=10

banner "sim_source control after a rejoin E2E"

prepare_ent_workspace control_after_rejoin "$BASE_DIR" 9153 4266 "$RACK_NAME"
RACK_STDOUT="${WORK_DIR}/${RACK_STDOUT_NAME}"
API_URL="http://127.0.0.1:${API_PORT}/api/v1"

# How many messages the encoder packed. The Rack's log repeats an earlier event, with the
# same content, when telemetry starts, so the messages are counted by their identifier.
decoded() {
    grep -a "direction=encode.*mti=0100" "$RACK_STDOUT" 2>/dev/null | grep -ao 'flux_id=[^ ]*' | sort -u | wc -l
}

# How many times the gear was started. The same repetition applies: count timestamps.
gear_starts() {
    grep -a "gear started.*name=${GEAR}\$" "$RACK_STDOUT" 2>/dev/null | awk -F' [|] ' '{print $1}' | sort -u | wc -l
}

# Usage: sim_command ACTION
sim_command() {
    local action="$1" status
    status=$(curl -s -o "${WORK_DIR}/control.out" -w "%{http_code}" --max-time 5 -X POST \
        "${API_URL}/control/sim/${action}" -H "Content-Type: application/json" \
        -d "{\"gear\": \"${GEAR}\"}")
    if [ "$status" != "200" ]; then
        cat "${WORK_DIR}/control.out" >&2
        fail "sim.${action} was refused (HTTP $status)"
    fi
}

# Usage: wait_decoded_at_least N TIMEOUT
wait_decoded_at_least() {
    local want="$1" deadline=$((SECONDS + $2))
    while (( SECONDS < deadline )); do
        [ "$(decoded)" -ge "$want" ] && return 0
        sleep 0.2
    done
    return 1
}

# Usage: wait_decoded_settled TIMEOUT
# Returns when two reads half a second apart agree: the generation has stopped.
wait_decoded_settled() {
    local deadline=$((SECONDS + $1)) last now
    last=$(decoded)
    while (( SECONDS < deadline )); do
        sleep 0.5
        now=$(decoded)
        [ "$now" == "$last" ] && return 0
        last="$now"
    done
    return 1
}

FAILS=0
check() {
    if [ "$1" -eq 0 ]; then
        log_success "$2"
    else
        log_error "$2"
        FAILS=$((FAILS + 1))
    fi
}

# 1. The Mixer is up, the Rack takes the scenario and keeps its copy. Nothing generates.
section "Phase 1: deploy the scenario with the Mixer up"
start_ent_mixer
start_ent_rack "$RACK_NAME"
import_scenario "${BASE_DIR}/scenario.yaml"
wait_for_file "${WORK_DIR}/rack/data/scenario.flux" 20 || fail "The Rack kept no copy of the scenario."
wait_for_log "$RACK_STDOUT" "gear started.*name=${GEAR}\$" "$ENROLL_TIMEOUT" || fail "The gear ${GEAR} did not start."
[ "$(decoded)" -eq 0 ]
check $? "The gear ${GEAR} is idle until a command reaches it (decoded $(decoded))."

# 2. Both go down. The Rack starts alone and resumes the scenario: the gear starts with no
# Mixer, so it asks for its control channel before there is a bus to give it one.
section "Phase 2: the Rack starts while the Mixer is away"
stop_ent_rack
stop_ent_mixer
SEEN=$(wc -l < "$RACK_STDOUT")
start_ent_rack_alone
if wait_for_log "$RACK_STDOUT" "Resumed last scenario from local state" "$RESUME_TIMEOUT" "$SEEN"; then
    check 0 "The Rack resumed its saved scenario with no Mixer."
else
    tail -30 "$RACK_STDOUT" >&2
    fail "The Rack did not resume its saved scenario without the Mixer."
fi
wait_for_log "$RACK_STDOUT" "gear started.*name=${GEAR}\$" "$RESUME_TIMEOUT" "$SEEN" \
    || fail "The gear ${GEAR} did not start without the Mixer."
STARTS_BEFORE=$(gear_starts)
log_info "The gear ${GEAR} was started ${STARTS_BEFORE} time(s) so far, counting phase 1."

# 3. The Mixer returns and the Rack joins it, keeping the gears it has.
section "Phase 3: the Mixer returns"
SEEN=$(wc -l < "$RACK_STDOUT")
restart_ent_mixer
if wait_for_log "$RACK_STDOUT" "Keeping the gears the previous session left running" "$REJOIN_TIMEOUT" "$SEEN"; then
    check 0 "The Rack joined the Mixer and kept its gears."
else
    tail -30 "$RACK_STDOUT" >&2
    fail "The Rack did not join the Mixer."
fi
wait_for_rack "${API_URL}" "$RACK_NAME" "$ENROLL_TIMEOUT" || fail "The Mixer never listed ${RACK_NAME} as active."
STARTS_AFTER=$(gear_starts)
[ "$STARTS_AFTER" == "$STARTS_BEFORE" ]
check $? "Joining the Mixer did not start ${GEAR} again (${STARTS_BEFORE} starts before, ${STARTS_AFTER} after)."
[ "$(decoded)" -eq 0 ]
check $? "The gear is still idle, so anything decoded from here on came from the command (decoded $(decoded))."

# 4. The Mixer commands the gear that was already running.
section "Phase 4: sim.start reaches the gear that ran before the rejoin"
SEEN=$(wc -l < "$RACK_STDOUT")
sim_command start
wait_for_log "$RACK_STDOUT" "Received sim.start command" "$COMMAND_TIMEOUT" "$SEEN"
check $? "The gear received the Mixer's sim.start."
wait_decoded_at_least "$MIN_DECODED" "$COMMAND_TIMEOUT"
check $? "The command made ${GEAR} generate: the encoder packed $(decoded) messages (wanted at least ${MIN_DECODED})."

# 5. And it stops on command.
section "Phase 5: sim.stop reaches it too"
SEEN=$(wc -l < "$RACK_STDOUT")
sim_command stop
wait_for_log "$RACK_STDOUT" "Received sim.stop command" "$COMMAND_TIMEOUT" "$SEEN"
check $? "The gear received the Mixer's sim.stop."
wait_decoded_settled "$COMMAND_TIMEOUT"
check $? "The generation stopped: the decoder count no longer moves."

if grep -aqiE "panic:|nil pointer|unknown gear type" "$RACK_STDOUT"; then
    log_error "The Rack logged a panic or an unknown gear type."
    FAILS=$((FAILS + 1))
else
    log_success "No panic and no unknown gear type in the Rack log."
fi

if [ "$FAILS" -eq 0 ]; then
    banner "CONTROL AFTER REJOIN E2E PASSED"
    exit 0
fi
banner "CONTROL AFTER REJOIN E2E FAILED ($FAILS checks)"
exit 1

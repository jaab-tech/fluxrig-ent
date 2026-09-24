#!/bin/bash
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
#
# Shared setup for the enterprise e2e tests.
#
# These tests start a real Mixer and a real Rack, both built with the enterprise
# gears, and check what a deployment of those gears does. They use the engine's
# own e2e helpers to start processes, wait on ports and print results, so they
# read like the engine's tests. Two settings connect them to the engine:
#
#   FLUXRIG_DIR      the engine checkout (default: ../fluxrig)
#   FLUXRIG_BIN_DIR  the directory with the binaries under test, named as the
#                    engine names them. make test-e2e builds it.

ENT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FLUXRIG_DIR="${FLUXRIG_DIR:-${ENT_ROOT}/../fluxrig}"
if [ ! -f "${FLUXRIG_DIR}/test/e2e/utils/e2e_utils.sh" ]; then
    echo "The engine checkout was not found at ${FLUXRIG_DIR}. Set FLUXRIG_DIR." >&2
    exit 1
fi
FLUXRIG_DIR="$(cd "${FLUXRIG_DIR}" && pwd)"

source "${FLUXRIG_DIR}/test/e2e/utils/e2e_utils.sh"
ROOT_DIR="${FLUXRIG_DIR}"

export FLUXRIG_BIN_DIR="${FLUXRIG_BIN_DIR:-${ENT_ROOT}/bin/engine-names}"
[ -x "$(bin_dir)/fluxrig" ] || fail "No enterprise binaries in $(bin_dir). Run these tests with make test-e2e."

MIXER_PID=""
RACK_PID=""
API_PORT=""

# The spec is stored under the sha256 of its content, which is how the Mixer
# resolves a reference such as iso8583-v87-ascii:v2.2.0.
seed_spec_store() {
    local spec="${FLUXRIG_DIR}/examples/specs/iso8583-v87-ascii.yaml"
    local sum
    if command -v sha256sum >/dev/null 2>&1; then
        sum=$(sha256sum "$spec" | cut -d' ' -f1)
    else
        sum=$(shasum -a 256 "$spec" | cut -d' ' -f1)
    fi
    mkdir -p "${WORK_DIR}/mixer/data/store/blobs/${sum:0:2}"
    cp "$spec" "${WORK_DIR}/mixer/data/store/blobs/${sum:0:2}/${sum}"
    printf '{"Specs": {"iso8583-v87-ascii": {"v2.2.0": "%s"}}, "Scenarios": {}}' "$sum" \
        > "${WORK_DIR}/mixer/data/store/index.json"
}

# Usage: prepare_ent_workspace $NAME $TEST_DIR $API_PORT $SNAKE_PORT $RACK_NAME
# Every test picks its own ports, so a straggler from one cannot break the next.
prepare_ent_workspace() {
    local name="$1" test_dir="$2" snake="$4" rack="$5"
    API_PORT="$3"

    lsof -ti :"$API_PORT" -ti :"$snake" 2>/dev/null | xargs kill -9 2>/dev/null || true
    setup_workspace "$name" "$test_dir"
    local c
    for c in mixer rack; do
        sed -e "s/@API_PORT@/${API_PORT}/g" -e "s/@SNAKE_PORT@/${snake}/g" -e "s/@RACK_NAME@/${rack}/g" \
            "${ENT_ROOT}/test/e2e/common/${c}.toml" > "${WORK_DIR}/${c}/fluxrig.toml"
    done
    seed_spec_store
}

# Usage: start_ent_mixer [$MIXER_BINARY]
# The default is the enterprise Mixer. A test that compares builds passes another.
start_ent_mixer() {
    local mixer_bin="${1:-$(bin_dir)/fluxrig-mixer}"
    ( cd "${WORK_DIR}/mixer" && "$(bin_dir)/fluxrig" keys gen-cluster -o ./data/cluster.key >/dev/null 2>&1 ) \
        || fail "cluster key generation failed"
    ( cd "${WORK_DIR}/mixer" && exec "$mixer_bin" -c fluxrig.toml > mixer.stdout 2>&1 ) &
    MIXER_PID=$!
    wait_for_port "$API_PORT" 20 || fail "Mixer failed to start: $(tail -3 "${WORK_DIR}/mixer/mixer.stdout" 2>/dev/null)"
}

# Usage: start_ent_rack $RACK_NAME
start_ent_rack() {
    local rack="$1"
    ( cd "${WORK_DIR}/rack" && mkdir -p logs && exec env FLUXRIG_TRACE=1 "$(bin_dir)/fluxrig" run -c fluxrig.toml > rack.stdout 2>&1 ) &
    RACK_PID=$!
    wait_for_rack "http://127.0.0.1:${API_PORT}/api/v1" "$rack" 30
    # Give enrollment time to finish before a scenario is pushed to the Rack.
    sleep 3
}

# Usage: restart_ent_mixer
# Starts the enterprise Mixer again on the data of the one that stopped: the same cluster
# key, so the Racks it enrolled still trust it. Its output is appended to mixer.stdout.
restart_ent_mixer() {
    ( cd "${WORK_DIR}/mixer" && exec "$(bin_dir)/fluxrig-mixer" -c fluxrig.toml >> mixer.stdout 2>&1 ) &
    MIXER_PID=$!
    wait_for_port "$API_PORT" 20 || fail "Mixer failed to restart: $(tail -3 "${WORK_DIR}/mixer/mixer.stdout" 2>/dev/null)"
}

# Usage: start_ent_rack_alone
# Starts the Rack without waiting for the Mixer, which may not be running. Its output is
# appended to rack.stdout, so the log of an earlier run stays and a test can read only
# what is new.
start_ent_rack_alone() {
    ( cd "${WORK_DIR}/rack" && mkdir -p logs && exec env FLUXRIG_TRACE=1 "$(bin_dir)/fluxrig" run -c fluxrig.toml >> rack.stdout 2>&1 ) &
    RACK_PID=$!
}

# Usage: stop_ent_rack, stop_ent_mixer
# Kills one of the two and waits for it, so its ports are free when the call returns.
stop_ent_rack() {
    [ -n "$RACK_PID" ] && { kill -9 "$RACK_PID" 2>/dev/null; wait "$RACK_PID" 2>/dev/null; }
    RACK_PID=""
}
stop_ent_mixer() {
    [ -n "$MIXER_PID" ] && { kill -9 "$MIXER_PID" 2>/dev/null; wait "$MIXER_PID" 2>/dev/null; }
    MIXER_PID=""
}

# Usage: import_scenario $FILE
import_scenario() {
    local status
    status=$(curl -s -o "${WORK_DIR}/import.out" -w "%{http_code}" -X POST \
        "http://127.0.0.1:${API_PORT}/api/v1/scenario/import?activate=true" \
        -H "Content-Type: application/x-yaml" --data-binary @"$1")
    if [ "$status" != "200" ]; then
        cat "${WORK_DIR}/import.out" >&2
        fail "scenario import failed (HTTP $status)"
    fi
}

# Stops what a test started. A test installs it with: trap stop_ent EXIT
stop_ent() {
    [ -n "$RACK_PID" ] && kill "$RACK_PID" 2>/dev/null
    [ -n "$MIXER_PID" ] && kill "$MIXER_PID" 2>/dev/null
    wait 2>/dev/null
    RACK_PID=""
    MIXER_PID=""
}

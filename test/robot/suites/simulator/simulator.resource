# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1

*** Settings ***
Documentation     Shared keywords for simulator test suites.
Library           ISO8583Library
Library           fluxrigLibrary
Library           Collections
Library           String
Library           OperatingSystem

*** Variables ***
${ISO_HOST}      localhost
${ISO_PORT}      8583
${SIM_PORT}      18583
${RESPONDER_PORT}=    10002
${REPORT_DIR}    ${CURDIR}/../results

*** Keywords ***
# Simulator-specific message handling

Send Message To Simulator
    [Arguments]    ${message_hex}    ${target}=${ISO_HOST}:${RESPONDER_PORT}    ${wait}=${EMPTY}
    # wait maps to the socket timeout: delay tests pass wait=1 and
    # assert on elapsed wall time.
    ${timeout}=    Set Variable If    '${wait}' == '${EMPTY}'    10    ${wait}
    ${reply_hex}=    Send Iso Message    ${message_hex}    target=${target}    header_len=2    timeout=${timeout}
    RETURN    ${reply_hex}

Send Message To Simulator Expecting No Reply
    [Arguments]    ${message_hex}    ${target}=${ISO_HOST}:${RESPONDER_PORT}    ${wait}=3
    Send Iso Message Expecting No Reply    ${message_hex}    target=${target}    wait=${wait}

Build Sim Request
    [Arguments]    ${mti}    @{fields}
    ${msg_hex}=    Build Iso Message    ${mti}    @{fields}
    RETURN    ${msg_hex}

Build Auth Request
    [Arguments]    ${pan}=4111111111111111    ${amount}=10000    ${stan}=000001    ${terminal}=TERM0001    ${expire}=${EMPTY}    ${bitmap}=binary    &{extra}
    &{fields}=    Create Dictionary    f2=${pan}    f4=${amount}    f11=${stan}    f41=${terminal}
    Run Keyword If    '${expire}' != '${EMPTY}'    Set To Dictionary    ${fields}    f14=${expire}
    FOR    ${k}    IN    @{extra}
        Set To Dictionary    ${fields}    ${k}=${extra}[${k}]
    END
    ${msg_hex}=    Build Iso Message    0100    bitmap=${bitmap}    &{fields}
    RETURN    ${msg_hex}

Build Financial Request
    [Arguments]    ${pan}=4111111111111111    ${amount}=5000    ${stan}=000001    ${terminal}=TERM0001
    ${msg_hex}=    Build Iso Message    0200    f2=${pan}    f4=${amount}    f11=${stan}    f41=${terminal}
    RETURN    ${msg_hex}

Build Network Mgmt Request
    [Arguments]    ${code}=301    ${bitmap}=binary
    # No fields: the assertion is MTI pairing (0800->0810), and field
    # 70 would need a secondary bitmap, which this stack cannot emit
    # (moov Length-16 bitmaps always occupy 32 wire chars; a real
    # secondary would need 64). Recorded as interop debt with the
    # bitmap quirk above.
    ${msg_hex}=    Build Iso Message    0800    bitmap=${bitmap}
    RETURN    ${msg_hex}

Parse Response
    [Arguments]    ${raw_hex}
    ${decoded}=    Decode Iso Message    ${raw_hex}
    RETURN    ${decoded}

Get Response Code
    [Arguments]    ${decoded}
    ${code}=    Get From Dictionary    ${decoded}    iso8583.field.39
    RETURN    ${code}

Get STAN
    [Arguments]    ${decoded}
    ${stan}=    Get From Dictionary    ${decoded}    iso8583.field.11
    RETURN    ${stan}

Get Auth Code
    [Arguments]    ${decoded}
    ${auth}=    Get From Dictionary    ${decoded}    iso8583.field.38
    RETURN    ${auth}

Verify Response Approved
    [Arguments]    ${decoded}
    ${code}=    Get Response Code    ${decoded}
    Should Be Equal    ${code}    00

Verify Response Declined
    [Arguments]    ${decoded}    ${expected_code}=51
    ${code}=    Get Response Code    ${decoded}
    Should Be Equal    ${code}    ${expected_code}

Verify STAN Echoed
    [Arguments]    ${decoded}    ${expected_stan}
    ${stan}=    Get STAN    ${decoded}
    Should Be Equal    ${stan}    ${expected_stan}

Verify Auth Code Present
    [Arguments]    ${decoded}
    ${auth}=    Get Auth Code    ${decoded}
    Should Not Be Empty    ${auth}
    Should Match Regexp    ${auth}    ^[A-Z0-9]{6}$

# Simulator control plane keywords
# NOTE: the argument is named ${gear} (not ${gear_name}) because every
# caller passes gear=<name>; a mismatched name lands the literal
# "gear=<name>" string in the body and the command reaches no gear.
# Numeric fields are converted: the Mixer decodes seed/tps/from/to as
# numbers and rejects quoted strings with 400.
Start Simulator
    [Arguments]    ${gear}    ${seed}=${EMPTY}
    ${body}=    Create Dictionary    gear=${gear}
    ${seed_int}=    Run Keyword If    '${seed}' != '${EMPTY}'    Convert To Integer    ${seed}
    Run Keyword If    '${seed}' != '${EMPTY}'    Set To Dictionary    ${body}    seed=${seed_int}
    ${resp}=    Post JSON    /api/v1/control/sim/start    ${body}
    Should Be Equal As Strings    ${resp}    {"status":"started"}

Stop Simulator
    [Arguments]    ${gear}
    ${body}=    Create Dictionary    gear=${gear}
    ${resp}=    Post JSON    /api/v1/control/sim/stop    ${body}
    Should Be Equal As Strings    ${resp}    {"status":"stopped"}

Change Simulator Rate
    [Arguments]    ${gear}    ${tps}    ${shape}=constant    ${from}=${EMPTY}    ${to}=${EMPTY}    ${over}=${EMPTY}
    ${tps_num}=    Convert To Number    ${tps}
    ${body}=    Create Dictionary    gear=${gear}    tps=${tps_num}    shape=${shape}
    ${from_num}=    Run Keyword If    '${from}' != '${EMPTY}'    Convert To Number    ${from}
    Run Keyword If    '${from}' != '${EMPTY}'    Set To Dictionary    ${body}    from=${from_num}
    ${to_num}=    Run Keyword If    '${to}' != '${EMPTY}'    Convert To Number    ${to}
    Run Keyword If    '${to}' != '${EMPTY}'    Set To Dictionary    ${body}    to=${to_num}
    Run Keyword If    '${over}' != '${EMPTY}'    Set To Dictionary    ${body}    over=${over}
    ${resp}=    Post JSON    /api/v1/control/sim/rate    ${body}
    Should Be Equal As Strings    ${resp}    {"status":"rate_changed"}

Reset Simulator
    [Arguments]    ${gear}    ${seed}=${EMPTY}
    ${body}=    Create Dictionary    gear=${gear}
    ${seed_int}=    Run Keyword If    '${seed}' != '${EMPTY}'    Convert To Integer    ${seed}
    Run Keyword If    '${seed}' != '${EMPTY}'    Set To Dictionary    ${body}    seed=${seed_int}
    ${resp}=    Post JSON    /api/v1/control/sim/reset    ${body}
    Should Be Equal As Strings    ${resp}    {"status":"reset"}

Post JSON
    [Arguments]    ${endpoint}    ${body}
    ${json}=    Evaluate    json.dumps($body)    modules=json
    ${resp}=    Run Process    curl    -s    -f    -X    POST    http://127.0.0.1:18080${endpoint}    -H    Content-Type: application/json    -d    ${json}
    RETURN    ${resp.stdout}

# Readiness probe: after a mid-suite scenario import the sink gear
# restarts and its listener drops. Capturing before the rebind lands
# on a dying socket ("connection closed", zero bytes). Poll the port
# instead of sleeping a fixed guess.
Wait For Sink Ready
    [Arguments]    ${port}=18583    ${timeout}=30s
    Wait Until Keyword Succeeds    ${timeout}    1s    Probe Sink Port    ${port}

Probe Sink Port
    [Arguments]    ${port}
    Evaluate    __import__('socket').create_connection(('127.0.0.1', int($port)), timeout=2).close()

# Persistent socket connection keywords for simulator capture
Open Simulator Capture
    [Arguments]    ${alias}=sim_capture    ${port}=${SIM_PORT}    ${timeout}=10
    Open Socket Connection    ${alias}    host=127.0.0.1    port=${port}    timeout=${timeout}

Read Simulator Message
    [Arguments]    ${alias}=sim_capture    ${header_len}=2    ${timeout}=10
    ${data}=    Read From Socket Connection    ${alias}    header_len=${header_len}    timeout=${timeout}
    RETURN    ${data}

Close Simulator Capture
    [Arguments]    ${alias}=sim_capture
    Close Socket Connection    ${alias}

Read N Simulator Messages
    [Arguments]    ${alias}=sim_capture    ${count}=5    ${header_len}=2    ${timeout}=10
    # Reads from an already-open capture connection, unlike Capture Messages,
    # which opens and closes one of its own: use this when the caller needs
    # to control exactly when the connection opens relative to when
    # generation starts, e.g. to connect before sending sim.start rather than
    # starting first and sleeping past a fixed head start.
    ${messages}=    Create List
    FOR    ${i}    IN RANGE    ${count}
        ${raw}=    Read Simulator Message    alias=${alias}    header_len=${header_len}    timeout=${timeout}
        ${msg}=    Parse Raw Message    ${raw}
        Append To List    ${messages}    ${msg}
    END
    RETURN    ${messages}

Wait For Raw Message
    [Arguments]    ${port}    ${timeout}=5
    ${data}=    Read From Socket    host=127.0.0.1    port=${port}    timeout=${timeout}
    RETURN    ${data}

Parse Raw Message
    [Arguments]    ${raw_hex}
    ${msg}=    Parse Iso Message    ${raw_hex}
    RETURN    ${msg}

Parse Iso Message
    [Arguments]    ${raw_hex}
    ${decoded}=    Run Keyword    Decode Iso Message    ${raw_hex}
    RETURN    ${decoded}

Compare Message Sequences
    [Arguments]    ${seq1}    ${seq2}
    # Wall-clock fields advance between captures by design (the
    # generator stamps date/time/datetime kinds from the system clock:
    # fields 7, 12, 13, 14 in the v87 spec); determinism covers
    # everything else. Exclude them.
    ${norm1}=    Evaluate    [{k: v for k, v in m.items() if k not in ('iso8583.field.7', 'iso8583.field.12', 'iso8583.field.13', 'iso8583.field.14')} for m in $seq1]
    ${norm2}=    Evaluate    [{k: v for k, v in m.items() if k not in ('iso8583.field.7', 'iso8583.field.12', 'iso8583.field.13', 'iso8583.field.14')} for m in $seq2]
    ${equal}=    Evaluate    $norm1 == $norm2
    RETURN    ${equal}

Count MTIs
    [Arguments]    ${messages}
    ${counts}=    Create Dictionary
    FOR    ${msg}    IN    @{messages}
        ${mti}=    Get From Dictionary    ${msg}    iso8583.mti
        ${count}=    Get From Dictionary    ${counts}    ${mti}    0
        ${count}=    Evaluate    ${count} + 1
        Set To Dictionary    ${counts}    ${mti}    ${count}
    END
    RETURN    ${counts}

Is Luhn Valid
    [Arguments]    ${pan}
    ${pan}=    Remove String Using Regexp    ${pan}    \D
    Run Keyword If    '${pan}' == '${EMPTY}'
    ...    Set Variable    ${False}
    ...    RETURN
    ${sum}=    Set Variable    0
    ${pan_len}=    Get Length    ${pan}
    # Standard Luhn: double every second digit from RIGHT (starting from second-to-last)
    # Doubled digits: indices pan_len-2, pan_len-4, ..., 0 (step -2)
    FOR    ${i}    IN RANGE    ${pan_len - 2}    -1    -2
        ${idx}=    Convert To Integer    ${i}
        ${d}=    Convert To Integer    ${pan}[${idx}]
        # NOTE: one Evaluate, numeric throughout. The old form compared
        # '${d}' > '9' as STRINGS ('12' > '9' is False) and Set Variable
        # stored the literal text "N - 9", so any doubled digit over 9
        # poisoned the sum. Pinned PANs (all 1s) never triggered it.
        ${d}=    Evaluate    (${d} * 2 - 9) if ${d} * 2 > 9 else ${d} * 2
        ${sum}=    Evaluate    ${sum} + ${d}
    END
    # Non-doubled digits: indices pan_len-1, pan_len-3, ..., 1 (step -2)
    FOR    ${i}    IN RANGE    ${pan_len - 1}    -1    -2
        ${idx}=    Convert To Integer    ${i}
        ${d}=    Convert To Integer    ${pan}[${idx}]
        ${sum}=    Evaluate    ${sum} + ${d}
    END
    ${valid}=    Evaluate    ${sum} % 10 == 0
    RETURN    ${valid}

# Helper to clean PAN for Luhn check
Clean PAN
    [Arguments]    ${pan}
    ${clean}=    Remove String Using Regexp    ${pan}    \D
    RETURN    ${clean}

# Simulator suite setup/teardown (shared)
Initialize Simulator Suite
    # NOTE: the argument is ${scenario_filename}, not ${scenario_file}:
    # Robot variable names are case-insensitive, so ${scenario_file}
    # would collide with the ${SCENARIO_FILE} suite variable set below.
    [Arguments]    ${suite_path}    ${scenario_filename}=scenario_sim_source.yaml
    Force Cleanup Environment
    ${wd}=    Setup Workspace    ${suite_path}    output_dir=${OUTPUT_DIR}
    Set Suite Variable    ${WORK_DIR}    ${wd}

    Set Suite Variable    ${MIXER_CONFIG}    ${suite_path}/configs/mixer/fluxrig-mixer.toml
    Set Suite Variable    ${RACK_CONFIG}     ${suite_path}/configs/rack/iso_rack.toml
    Set Suite Variable    ${SCENARIO_FILE}   ${suite_path}/${scenario_filename}

    Copy File    ${suite_path}/specs/rules.yaml    ${WORK_DIR}/rack/specs/rules.yaml
    Create Directory    ${WORK_DIR}/mixer/data/store/blobs/5f
    Copy File    %{FLUXRIG_DIR}/examples/specs/iso8583-v87-ascii.yaml    ${WORK_DIR}/mixer/data/store/blobs/5f/5f789c2114a45bd81f265339ddb6d96949fcdf26d752745ac2ddf83a4f0a0ac9
    Create File    ${WORK_DIR}/mixer/data/store/index.json    {"Specs": {"iso8583-v87-ascii": {"v2.2.0": "5f789c2114a45bd81f265339ddb6d96949fcdf26d752745ac2ddf83a4f0a0ac9"}}, "Scenarios": {}}

    Generate Cluster Key    work_dir=${WORK_DIR}/mixer
    Start Mixer    config_file=${MIXER_CONFIG}    work_dir=${WORK_DIR}/mixer    alias=mixer
    Sleep    15s
    Start Rack     config_file=${RACK_CONFIG}     work_dir=${WORK_DIR}/rack    mixer_home=${WORK_DIR}/mixer    alias=rack
    Wait For Rack Registration    mixer_port=${MIXER_PORT}    rack_name=sim-node-01    timeout=60
    Wait For Rack Active By Name    mixer_port=${MIXER_PORT}    rack_name=sim-node-01    timeout=60
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${SCENARIO_FILE}

    # Wait for sim_source to start generating and sink server to be ready.
    # The base scenario is on_load: no explicit start needed (and a
    # start sent before the gear boots is lost, no redelivery).
    Sleep    3s

Teardown Simulator Suite
    Stop All Processes
    Run Keyword And Continue On Failure    Check Log For Errors    ${WORK_DIR}/rack/logs/fluxrig.log

# Integration suite setup/teardown
Initialize Integration Suite
    [Arguments]    ${suite_path}
    Initialize Simulator Suite    ${suite_path}    scenario_integration.yaml

Teardown Integration Suite
    Teardown Simulator Suite

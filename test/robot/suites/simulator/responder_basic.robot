# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1

*** Settings ***
Documentation     sim_responder gear tests - verifies ISO8583 authorizer simulation,
...               echo semantics, conditional rules, delay override.
Resource          %{FLUXRIG_DIR}/test/robot/resources/common.resource
Resource          ./simulator.resource
Library           fluxrigLibrary
Library           ISO8583Library
Library           Collections
Library           String
Library           OperatingSystem
Suite Setup       Initialize Sim Responder Suite    ${CURDIR}
Suite Teardown    Teardown Sim Responder Suite

*** Variables ***
${MIXER_PORT}     18080
${RESPONDER_PORT}=    10002
${WORK_DIR}       ${EMPTY}
${MIXER_CONFIG}   ${CURDIR}/configs/mixer/fluxrig-mixer.toml
${RACK_CONFIG}    ${CURDIR}/configs/rack/iso_rack.toml
${SCENARIO_FILE}  ${CURDIR}/scenario_sim_responder.yaml
${SILENCE}        4

*** Test Cases ***
Responder Returns Approved For Valid Request
    [Documentation]    A valid authorization request should get an approved response.
    [Tags]    responder    approval
    ${req}=    Build Auth Request    bitmap=hex
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    ${mti}=    Get From Dictionary    ${decoded}    iso8583.mti
    Should Be Equal    ${mti}    0110

    # Check default response fields
    Verify Response Approved    ${decoded}
    Verify Auth Code Present    ${decoded}
    # RRN is response-mandatory: YYMMDD + 6-digit sequence. The
    # backslash is doubled: Robot eats a single one before re sees it.
    ${rrn}=    Get From Dictionary    ${decoded}    iso8583.field.37
    Should Match Regexp    ${rrn}    ^\\d{12}$

Responder Echoes STAN Correctly
    [Documentation]    STAN (DE 11) should be echoed in response.
    [Tags]    responder    echo
    ${req}=    Build Auth Request    bitmap=hex    stan=123456
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    ${stan}=    Get From Dictionary    ${decoded}    iso8583.field.11
    Should Be Equal    ${stan}    123456

Responder Echoes Amount With Response Value Modified
    [Documentation]    Amount (DE 4) should be echoed (response_value: modified).
    [Tags]    responder    echo
    ${req}=    Build Auth Request    bitmap=hex    amount=5000
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    ${amount}=    Get From Dictionary    ${decoded}    iso8583.field.4
    Should Be Equal As Integers    ${amount}    5000

Responder Does Not Echo PAN In Response MTI
    [Documentation]    PAN (DE 2) should NOT be echoed in 0110 per spec.
    [Tags]    responder    echo
    ${req}=    Build Auth Request    bitmap=hex    pan=4111111111111111
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    ${pan}=    Get From Dictionary    ${decoded}    iso8583.field.2    default=${EMPTY}
    Should Be Empty    ${pan}

Responder Insufficient Funds Rule
    [Documentation]    Amount > 1M should trigger insufficient funds (code 51).
    [Tags]    responder    rules    conditional
    ${req}=    Build Auth Request    bitmap=hex    amount=2000000
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    Verify Response Declined    ${decoded}    51

Responder Expired Card Rule
    [Documentation]    Expired card (DE 14 < 2501) should trigger code 54.
    [Tags]    responder    rules    conditional
    ${req}=    Build Auth Request    bitmap=hex    expire=2401
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    Verify Response Declined    ${decoded}    54

Responder Fraud Suspect Rule
    [Documentation]    Field 48 == RS01 should trigger fraud code 59.
    [Tags]    responder    rules    conditional
    ${req}=    Build Auth Request    bitmap=hex    f48=RS01
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    Verify Response Declined    ${decoded}    59

Responder No Signal Approves Anyway
    [Documentation]    Missing signal (RS11/RS12) should still approve.
    [Tags]    responder    rules    conditional
    ${req}=    Build Auth Request    bitmap=hex    f48=RS12
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    Verify Response Approved    ${decoded}

Responder Rule Delay Override
    [Documentation]    Rule delay should replace base delay, not add to it.
    [Tags]    responder    delay    override
    ${req}=    Build Auth Request    bitmap=hex    amount=2000000
    ${start}=    Evaluate    __import__('time').time()
    ${reply}=    Send Message To Simulator    ${req}    wait=5
    ${end}=    Evaluate    __import__('time').time()
    ${elapsed}=    Evaluate    ${end} - ${start}
    # Rule delay is 1ms, base is 50ms - should use 1ms not 51ms.
    # Float clock: epoch integers make sub-second bounds a coin flip.
    Should Be True    ${elapsed} < 0.5

Responder Default Delay Applied
    [Documentation]    When no rule matches, base delay should apply.
    [Tags]    responder    delay    default
    ${req}=    Build Auth Request    bitmap=hex    amount=1000
    ${start}=    Evaluate    __import__('time').time()
    ${reply}=    Send Message To Simulator    ${req}    wait=5
    ${end}=    Evaluate    __import__('time').time()
    ${elapsed}=    Evaluate    ${end} - ${start}
    # Base delay 50ms, should be around that
    Should Be True    ${elapsed} > 0.03
    Should Be True    ${elapsed} < 0.5

Responder Echoes Auth Code
    [Documentation]    Auth code should be generated for approved transactions.
    [Tags]    responder    auth
    ${req}=    Build Auth Request    bitmap=hex
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    Verify Auth Code Present    ${decoded}

Responder Returns Response MTI 0110 For 0100 Request
    [Documentation]    Response MTI should be pairs_with of request MTI.
    [Tags]    responder    mti
    ${req}=    Build Auth Request    bitmap=hex
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    ${mti}=    Get From Dictionary    ${decoded}    iso8583.mti
    Should Be Equal    ${mti}    0110

Responder Handles Network Management Request
    [Documentation]    0800 should get 0810 response.
    [Tags]    responder    network
    ${req}=    Build Network Mgmt Request    bitmap=hex
    ${reply}=    Send Message To Simulator    ${req}
    ${decoded}=    Parse Response    ${reply}
    ${mti}=    Get From Dictionary    ${decoded}    iso8583.mti
    Should Be Equal    ${mti}    0810

Responder Rule Delay Override Shorter Than Base
    [Documentation]    Rule delay should replace base delay entirely.
    [Tags]    responder    delay    override
    ${req}=    Build Auth Request    bitmap=hex    amount=2000000
    ${start}=    Evaluate    __import__('time').time()
    ${reply}=    Send Message To Simulator    ${req}    wait=5
    ${end}=    Evaluate    __import__('time').time()
    ${elapsed}=    Evaluate    ${end} - ${start}
    # Rule delay is 1ms (from rule definition), base is 50ms
    Should Be True    ${elapsed} < 0.5

*** Keywords ***
Initialize Sim Responder Suite
    [Arguments]    ${suite_path}
    Force Cleanup Environment
    ${wd}=    Setup Workspace    ${suite_path}    output_dir=${OUTPUT_DIR}
    Set Suite Variable    ${WORK_DIR}    ${wd}

    Set Suite Variable    ${MIXER_CONFIG}    ${suite_path}/configs/mixer/fluxrig-mixer.toml
    Set Suite Variable    ${RACK_CONFIG}     ${suite_path}/configs/rack/iso_rack.toml
    Set Suite Variable    ${SCENARIO_FILE}   ${suite_path}/scenario_sim_responder.yaml

    Copy File    ${suite_path}/specs/rules.yaml    ${WORK_DIR}/rack/specs/rules.yaml
    # Seed the mixer CAS with the v87 spec blob, same as the source
    # suite: without it the responder gear cannot resolve its
    # iso8583-v87-ascii:v2.2.0 store reference and Init fails.
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

    Wait For Port    port=10002    timeout=60

Teardown Sim Responder Suite
    Stop All Processes
    Run Keyword And Continue On Failure    Check Log For Errors    ${WORK_DIR}/rack/logs/fluxrig.log

Post JSON
    [Arguments]    ${endpoint}    ${body}
    ${json}=    Convert To JSON    ${body}
    ${resp}=    Run Process    curl    -s    -f    -X    POST    http://127.0.0.1:18080${endpoint}    -H    Content-Type: application/json    -d    ${json}    return_stdout=true
    RETURN    ${resp}

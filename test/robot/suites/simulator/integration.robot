# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1

*** Settings ***
Documentation     Full integration test: sim_source -> enrichment Rack -> sim_responder.
...               Exercises the complete flow with enrichment logic in between.
Resource          %{FLUXRIG_DIR}/test/robot/resources/common.resource
Resource          ./simulator.resource
Library           fluxrigLibrary
Library           ISO8583Library
Library           Collections
Library           String
Library           OperatingSystem
Library           Process
Suite Setup       Initialize Integration Suite    ${CURDIR}
Suite Teardown    Teardown Integration Suite

*** Variables ***
${MIXER_PORT}     18080
${RACK_PORT}      18583
${WORK_DIR}       ${EMPTY}
${MIXER_CONFIG}   ${CURDIR}/configs/mixer/fluxrig-mixer.toml
${RACK_CONFIG}    ${CURDIR}/configs/rack/iso_rack.toml
${SCENARIO_FILE}  ${CURDIR}/scenario_integration.yaml
${SILENCE}        4
${SPEC_REF}       iso8583-v87-ascii:v2.2.0

*** Test Cases ***
Integration Full Flow End To End
    [Documentation]    Complete flow: sim_source -> enrichment -> sim_responder -> response back.
    [Tags]    integration    e2e
    # Start the traffic generator via Control Plane
    Start Simulator    gear=scheme    seed=20260904

    # Wait for messages to flow through
    Sleep    10s

    # Capture some responses
    ${responses}=    Capture Responses    count=10    timeout=30

    # Verify responses are approved or properly declined. Capture
    # Responses already returns parsed dicts; parsing again would feed
    # a dict to the hex decoder. Any decline code the scenario rules
    # can produce is legitimate;
    # anything else fails the flow.
    FOR    ${resp}    IN    @{responses}
        ${code}=    Get Response Code    ${resp}
        Run Keyword If    '${code}' == '00'    Verify Response Approved    ${resp}
        ...    ELSE IF    '${code}' == '51'    Verify Response Declined    ${resp}    51
        ...    ELSE IF    '${code}' == '54'    Verify Response Declined    ${resp}    54
        ...    ELSE IF    '${code}' == '59'    Verify Response Declined    ${resp}    59
        ...    ELSE    Fail    Unexpected response code: ${code}
    END

    # Stop the generator
    Stop Simulator    gear=scheme

Integration Rate Shaping Works
    [Documentation]    Verify rate shaping works end-to-end.
    [Tags]    integration    rate
    Start Simulator    gear=scheme    seed=20260904

    ${start}=    Evaluate    __import__('time').time()
    ${responses}=    Capture Responses    count=50    timeout=120
    ${end}=    Evaluate    __import__('time').time()

    Stop Simulator    gear=scheme

    # End-to-end delivery at rate: all 50 arrive (the capture itself
    # enforces count), and the window only bounds stalls. No lower
    # bound: the ramp rate at this suite position is high enough that
    # 50 messages legitimately arrive in seconds. Float clock: epoch
    # integers make short windows a coin flip.
    ${n}=    Get Length    ${responses}
    Should Be True    ${n} == 50
    ${duration}=    Evaluate    ${end} - ${start}
    Should Be True    ${duration} < 120

*** Keywords ***
Capture Responses
    [Arguments]    ${count}    ${timeout}=30
    # One persistent connection, not Wait For Raw Message's old
    # socket.create_connection-per-call: that forced the sink to hand off
    # its "sole active connection" once per message. Full Flow End To End
    # never noticed, because it sleeps 10s before capturing only 10
    # messages, which is enough of a buffer cushion to read from a
    # backlog rather than race live delivery; Rate Shaping Works reads 50
    # with no such cushion, against a rate that keeps climbing over the
    # capture's own duration, and the reconnect churn eventually raced the
    # handoff and a message fell into the gap between one connection
    # closing and the next being recognized: a hang, not a crash, since
    # nothing more ever arrives on the dead connection. A connection
    # opened once has no handoff left to race.
    Open Simulator Capture    alias=integration_capture    port=18584
    ${responses}=    Read N Simulator Messages    alias=integration_capture    count=${count}    timeout=${timeout}
    Close Simulator Capture    alias=integration_capture
    RETURN    ${responses}

# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1

*** Settings ***
Documentation     Basic sim_source gear tests - verifies deterministic message generation,
...               rate shaping, and correct field values.
Resource          %{FLUXRIG_DIR}/test/robot/resources/common.resource
Resource          ./simulator.resource
Library           BuiltIn
Library           fluxrigLibrary
Library           ISO8583Library
Library           Collections
Library           String
Library           OperatingSystem
Library           Process
Suite Setup       Initialize Simulator Suite    ${CURDIR}
Suite Teardown    Teardown Simulator Suite

*** Variables ***
${MIXER_PORT}     18080
${RACK_PORT}      18583
${WORK_DIR}       ${EMPTY}
${MIXER_CONFIG}   ${CURDIR}/configs/mixer/fluxrig-mixer.toml
${RACK_CONFIG}    ${CURDIR}/configs/rack/iso_rack.toml
${SCENARIO_FILE}  ${CURDIR}/scenario_sim_source.yaml
${SILENCE}        4
${SPEC_REF}       iso8583-v87-ascii:v2.2.0
${SEED}           20260904

*** Test Cases ***
Generator Produces Deterministic Output With Same Seed
    [Documentation]    Same seed must produce identical message sequences.
    [Tags]    deterministic    macros
    [Timeout]    240s
    # Determinism is asserted across a FRESH restart, not across a
    # live reset boundary: sink buffers, in-flight messages and
    # restart races make the boundary unalignable (no counter marks
    # it: field 11 is absent from these messages), while a restart
    # reproduces virgin state exactly. Each phase connects and starts
    # the readiness probe itself (Open Simulator Capture, then Start
    # Simulator) rather than starting first and sleeping past a fixed
    # head start: the previous version relied on the sink's
    # flush-on-connect behaviour "eating" exactly one buffered message
    # in the same way on both phases for the two 5-message windows to
    # land at the same stream offset, which is a race, not a
    # guarantee, and it broke exactly when it stopped holding (see
    # "Rate Shaping Constant Works" for the same class of bug, with a
    # timing budget instead of a message count). Connecting first
    # means both phases start reading from message 1, by construction.
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${CURDIR}/scenario_sim_control.yaml
    # HTTP 200 means accepted, not applied: sleep past the rack push
    # plus gear boot before probing, or the probe passes on the dying
    # listener and the capture lands in the rebind gap.
    Sleep    5s
    Wait For Sink Ready
    Open Simulator Capture    alias=sim_capture
    Start Simulator    gear=traffic
    ${msg1}=    Read N Simulator Messages    alias=sim_capture    count=5
    Close Simulator Capture    alias=sim_capture
    # Identical restart must reproduce the identical stream.
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${CURDIR}/scenario_sim_control.yaml
    Sleep    5s
    Wait For Sink Ready
    Open Simulator Capture    alias=sim_capture
    Start Simulator    gear=traffic
    ${msg2}=    Read N Simulator Messages    alias=sim_capture    count=5
    Close Simulator Capture    alias=sim_capture
    ${equal}=    Compare Message Sequences    ${msg1}    ${msg2}
    Should Be True    ${equal}
    # Reset command contract (delivery plus post-reset liveness).
    # Equality across the reset boundary itself is asserted by the
    # restart comparison above, not here.
    Reset Simulator    gear=traffic    seed=${SEED}
    ${alive}=    Capture Messages    count=2
    ${n}=    Get Length    ${alive}
    Should Be True    ${n} == 2

Generator Produces Different Output With Different Seeds
    [Documentation]    Different seeds must produce different message sequences.
    [Tags]    deterministic    macros
    [Timeout]    120s
    # Hermetic: the deterministic test above swaps scenarios (and may
    # die before restoring base), so re-establish base flow here.
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${SCENARIO_FILE}
    Sleep    5s
    Wait For Sink Ready
    ${msg1}=    Capture Messages    count=5    seed=11111111
    ${msg2}=    Capture Messages    count=5    seed=22222222
    ${equal}=    Compare Message Sequences    ${msg1}    ${msg2}
    Should Be Equal As Strings    ${equal}    False

Generator Produces Correct MTI Mix
    [Documentation]    The generated message mix should match configured weights.
    [Tags]    mix    macros
    [Timeout]    120s
    ${messages}=    Capture Messages    count=20
    ${mti_counts}=    Count MTIs    ${messages}
    ${mti_0100}=    Get From Dictionary    ${mti_counts}    0100
    ${mti_0200}=    Get From Dictionary    ${mti_counts}    0200
    ${mti_0800}=    Get From Dictionary    ${mti_counts}    0800    0
    # 85% 0100, 10% 0200, 5% 0800 - allow some variance
    ${total}=    Evaluate    ${mti_0100} + ${mti_0200} + ${mti_0800}
    Should Be True    ${mti_0100} / ${total} > 0.70
    Should Be True    ${mti_0100} / ${total} < 0.95
    Should Be True    ${mti_0200} / ${total} > 0.03
    Should Be True    ${mti_0200} / ${total} < 0.25

Generator Produces Valid PAN With Luhn Check
    [Documentation]    Generated PAN must pass Luhn check.
    [Tags]    macros    pan
    [Timeout]    120s
    ${messages}=    Capture Messages    count=5
    FOR    ${msg}    IN    @{messages}
        ${pan}=    Get From Dictionary    ${msg}    iso8583.field.2
        Log To Console    Raw PAN: ${pan}
        ${clean_pan}=    Clean PAN    ${pan}
        Log To Console    Clean PAN: ${clean_pan}
        ${is_valid}=    Is Luhn Valid    ${clean_pan}
        Should Be True    ${is_valid}
    END

Generator Produces Incrementing STAN
    [Documentation]    STAN should increment sequentially (field 11 in request messages).
    [Tags]    macros    stan
    [Timeout]    120s
    ${messages}=    Capture Messages    count=5
    # Presence is required, not optional: 0800s carry no STAN and are
    # skipped explicitly (a bare skip-if-missing passed vacuously for
    # months while the generator never emitted field 11 at all).
    ${checked}=    Set Variable    0
    ${prev_stan}=    Set Variable    -1
    FOR    ${msg}    IN    @{messages}
        ${mti}=    Get From Dictionary    ${msg}    iso8583.mti
        Run Keyword If    '${mti}' == '0800'    Continue For Loop
        ${stan}=    Get From Dictionary    ${msg}    iso8583.field.11
        ${stan_n}=    Convert To Integer    ${stan}
        Should Be True    ${stan_n} > ${prev_stan}
        ${prev_stan}=    Set Variable    ${stan_n}
        ${checked}=    Evaluate    ${checked} + 1
    END
    Should Be True    ${checked} > 0



Generator Produces Named Sequences
    [Documentation]    Named sequences should increment independently.
    [Tags]    macros    sequences
    [Timeout]    120s
    ${seq1}=    Capture Messages    count=5    seed=1000
    ${seq2}=    Capture Messages    count=5    seed=2000
    # The sequences should be different because they use different seeds
    ${diff}=    Run Keyword If    "${seq1}" != "${seq2}"    Set Variable    True
    Should Be True    ${diff}

Value Precedence Works Correctly
    [Documentation]    set > template > defaults > synthesis precedence.
    [Tags]    precedence    macros
    [Timeout]    120s
    # Create scenario with set pinning PAN (use alias 'pan' not 'card.pan')
    ${scenario}=    Create Scenario With Set    pan=4111111111111111
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${scenario}
    # HTTP 200 means accepted, not applied: let the rack push, stop
    # the old sink and rebind before probing, or the probe passes on
    # the dying listener and the capture lands in the rebind gap.
    Sleep    5s
    Wait For Sink Ready
    ${messages}=    Capture Messages    count=5
    FOR    ${msg}    IN    @{messages}
        ${pan}=    Get From Dictionary    ${msg}    iso8583.field.2
        Should Be Equal    4111111111111111    ${pan}
    END

Rate Shaping Constant Works
    [Documentation]    Constant rate should produce messages at steady rate.
    [Tags]    rate    constant
    [Timeout]    180s
    # on_control, not the base on_load scenario, and the capture connects
    # before generation starts. An on_load gear starts the moment the
    # scenario applies, and the sink buffers whatever it emits with nobody
    # connected yet (io_tcp's own documented behaviour); five seconds
    # of head start at 1 TPS is exactly enough to have all 5 messages this
    # test wants already sitting in that buffer before Capture Messages
    # ever opens a socket, so what got measured was buffer-drain speed,
    # not the generation rate the test claims to check. That went from
    # "usually true" to "always true" once gear startup got faster (this
    # session's sim_source fix removed a redundant packing step), which is
    # what turned a latent flaw into a consistent failure. Connecting
    # first and starting second makes every counted message genuinely
    # real-time, by construction rather than by how the timing happens to
    # fall.
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${CURDIR}/scenario_sim_control.yaml
    Sleep    5s
    Wait For Sink Ready
    Open Simulator Capture    alias=sim_capture
    Start Simulator    gear=traffic
    # Float clock: epoch integers make a ~5s measurement flaky at
    # the boundary (3 vs 4). time.time() measures true duration.
    ${start}=    Evaluate    __import__('time').time()
    ${messages}=    Read N Simulator Messages    alias=sim_capture    count=5
    Close Simulator Capture    alias=sim_capture
    ${end}=    Evaluate    __import__('time').time()
    ${duration}=    Evaluate    ${end} - ${start}
    # 5 messages at 1 TPS = ~5s, allow variance
    Should Be True    ${duration} > 3.0
    Should Be True    ${duration} < 10.0

Rate Shaping Ramp Works
    [Documentation]    Ramp rate should increase TPS over time.
    [Tags]    rate    ramp
    [Timeout]    120s
    ${scenario}=    Create Scenario With Ramp
    Import Scenario    mixer_port=${MIXER_PORT}    file_path=${scenario}
    # Same rebind race as above: sleep first, then probe.
    Sleep    5s
    Wait For Sink Ready
    ${start}=    Evaluate    __import__('time').time()
    ${messages}=    Capture Messages    count=200
    ${end}=    Evaluate    __import__('time').time()
    ${duration}=    Evaluate    ${end} - ${start}
    # 200 messages at ramp from 10 to 200 over 30s. Epoch-second
    # resolution plus loaded machines make a tight upper bound flaky;
    # 10s still catches a stuck gear (would take minutes or time out).
    Should Be True    ${duration} > 1.5
    Should Be True    ${duration} < 10.0

*** Keywords ***
Capture Messages
    [Arguments]    ${count}=50    ${seed}=${EMPTY}
    ${messages}=    Create List

    # A seed reseeds the running generator via the same Reset Simulator
    # control command the reset-contract test already trusts; without this
    # the keyword only ever read whatever the currently loaded scenario was
    # already generating, so two calls with different seeds compared two
    # arbitrary live batches instead of two seeded runs.
    Run Keyword If    '${seed}' != '${EMPTY}'    Reset Simulator    gear=traffic    seed=${seed}

    # Open persistent connection for this test case
    Open Simulator Capture    alias=sim_capture    port=${RACK_PORT}
    
    FOR    ${i}    IN RANGE    ${count}
        ${raw}=    Read Simulator Message    alias=sim_capture    header_len=2    timeout=10
        ${msg}=    Parse Raw Message    ${raw}
        Append To List    ${messages}    ${msg}
    END
    
    Close Simulator Capture    alias=sim_capture
    RETURN    ${messages}

Create Scenario With Set
    [Arguments]    ${pan}=4111111111111111
    ${epoch}=    Get Time    result_format=epoch
    ${path}=    Set Variable    /tmp/fluxrig/temp_scenario_${epoch}_set.yaml
    # The interpreter that runs Robot, not whichever python3 the PATH names first: the
    # script needs PyYAML, which the Robot environment declares.
    ${python}=    Evaluate    sys.executable    modules=sys
    ${result}=    OperatingSystem.Run    ${python} ${CURDIR}/../../scripts/write_scenario.py ${path} set ${pan}
    Log    Write set scenario result: ${result}
    RETURN    ${path}

Create Scenario With Ramp
    [Arguments]    ${seed}=20260904
    ${epoch}=    Get Time    result_format=epoch
    ${path}=    Set Variable    /tmp/fluxrig/temp_scenario_${epoch}_ramp.yaml
    ${python}=    Evaluate    sys.executable    modules=sys
    ${result}=    OperatingSystem.Run    ${python} ${CURDIR}/../../scripts/write_scenario.py ${path} ramp
    Log    Write ramp scenario result: ${result}
    RETURN    ${path}

Create Temp File
    [Arguments]    ${content}
    Log    Create Temp File called
    ${epoch}=    Get Time    result_format=epoch
    Log    Epoch: ${epoch}
    ${random}=    Generate Random String    8    LETTERS
    ${path}=    Set Variable    /tmp/fluxrig/temp_scenario_${epoch}_${random}.yaml
    Log    Path to create: ${path}
    Create Directory    /tmp/fluxrig
    Run Keyword And Ignore Error    Remove File    ${path}
    # Ensure content is a string, not a list
    ${content_str}=    Convert To String    ${content}
    Create File    ${path}    ${content_str}
    RETURN    ${path}
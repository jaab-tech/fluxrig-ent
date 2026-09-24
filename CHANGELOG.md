<!-- Copyright (c) 2026 JAAB Tech SAS, Uruguay All Rights Reserved -->

# Changelog

## [v0.1.0] - 2026-09-24

### Added
- `sim_source`: deterministic ISO8583 traffic generator, spec-driven
  synthesis, `$PAN`/`$STAN`/`$RRN`/`$SEQ`/`$RAND`/`$UUID`/`$NOW`/`$ENUM`/`$INVALID`/`$AUTH`
  macros, constant/ramp rate shapes (poisson and spike fail fast as roadmap),
  `on_load`/`on_control` triggers (`on_schedule` fails fast as roadmap),
  runtime `sim.start`/`sim.stop`/`sim.rate`/`sim.reset` control commands, kept live
  across any number of stops and restarts. It is a logic gear: it resolves a
  message's fields from the spec and emits a fields-only fluxMsg, and never packs
  or parses ISO8583 wire bytes itself. Reading each generated message's reply and
  scoring it against what the request should produce is `sim_validator`, a
  roadmap companion gear.
  A `timezone` setting (local, GMT or a zone name) chooses the clock that `$NOW` and
  `$RRN` render in both gears.
  A date or a time is written in the layout the spec declares for its field, and an
  expiry date (DE 14, `YYMM`) is generated `expiry_months` ahead (24 by default) so the
  card it belongs to has not expired.
  `expired_percent` makes a share of them already expired, drawn from the seed, for
  scenarios that exercise the decline for an expired card.
- `sim_responder`: authorizer simulator with spec-derived response MTIs,
  conditional decline rules with per-rule delay override, echo semantics.
- `fluxrig-ent` and `fluxrig-mixer-ent`: the engine's Rack and Mixer with these
  gears registered, built here with `make build`. The engine's own binaries do not
  contain them, and its repository does not name this module.
- Two kinds of test for the gears, both against the enterprise binaries and both
  covering only the enterprise gears. `make test-e2e` runs four shell tests that start
  a real Mixer and Rack: the gear catalog of the enterprise Mixer against the
  open-source one, `sim_source` generating traffic, `sim_responder` answering
  requests over a socket, and the Mixer's `sim.start` and `sim.stop` reaching a
  `sim_source` that was running before the Rack rejoined the Mixer. `make test-robot` runs 25 Robot tests that check the gears in
  detail, on the engine's Robot runner.
- `LICENSE`: Business Source License 1.1, no Additional Use Grant. Reading the
  source and any non-production use (development, testing, evaluation) is
  free. Production use, including offering the Licensed Work to third parties
  as a product, a service, or a hosted offering, needs a separate commercial
  licence. Each release converts to Apache License 2.0 four years after it
  ships.
- A signed licence file, checked once at startup by `fluxrig-mixer-ent` only (a
  Rack cannot run without enrolling to a Mixer, so checking there covers both).
  `FLUXRIG_LICENSE_FILE` names its path; unset, a Mixer looks for
  `fluxrig-license.json` next to where it runs. No file, an unverifiable one, or
  one whose window does not cover the running build logs a warning naming the
  affected gears (`sim_source`, `sim_responder`) as running in evaluation mode
  and points to `https://fluxrig.org/docs/enterprise/licensing`; it never blocks
  startup. Replacing the file takes effect on the Mixer's next restart.

Pairs with fluxrig v0.11.0.

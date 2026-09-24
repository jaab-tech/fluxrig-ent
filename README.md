<!-- Copyright (c) 2026 JAAB Tech SAS, Uruguay All Rights Reserved -->
<!-- See https://jaab.tech -->

# fluxrig-ent

Licensed gears for [fluxrig](https://fluxrig.org), the connectivity and
protocol orchestration platform for distributed mission-critical
infrastructure.

This repository is the **commercial side** of fluxrig. The engine, the SDK and
the gears that make a deployment work are Apache 2.0 and live in
[jaab-tech/fluxrig](https://github.com/jaab-tech/fluxrig). What lives here is
licensed under the [Business Source License 1.1](LICENSE): source-available,
with no Additional Use Grant. Converts to Apache 2.0 four years after each
release. See [licensing terms](https://fluxrig.org/docs/enterprise/licensing)
for the full picture.

This repository is **source-available, not open-source**. The source in it is
complete and readable, so a deployment can be audited (a binary you cannot
read is a conversation that ends early), but non-production use is the only
use this licence grants on its own: production use, including offering the
gears to third parties as a product, a service, or a hosted offering, needs a
commercial licence. Each release converts to Apache 2.0 four years after it
ships; the date is in the licence file, not a promise.

## Gears

| Gear | Role | Status |
|:---|:---|:---|
| `sim_source` | Deterministic ISO8583 traffic generator (macros, rate shaping, triggers) | Stable, shipped in v0.1.0 |
| `sim_responder` | Authorizer simulator (echo, conditional rules, delay override) | Stable, shipped in v0.1.0 |

Gear documentation is published at
[fluxrig.org](https://fluxrig.org/docs/reference/gears/overview), in the gear
reference catalog; the licence itself is covered in the
[enterprise section](https://fluxrig.org/docs/enterprise).

## Build

The gears ship inside two binaries: the engine's Rack and Mixer, with these gears
registered. `fluxrig-ent` is the Rack. `fluxrig-mixer-ent` is the Mixer whose gear
catalog lists them. Build both next to a checkout of the engine:

```
make build
```

The engine's own binaries, `fluxrig` and `fluxrig-mixer`, do not contain these
gears. The engine repository has no build target, tag or dependency that names this
module. This repository depends on the engine, and never the other way round.

## Develop

`go.mod` names a real, released version of the engine, so `make build` needs
nothing beyond this checkout. To develop against an engine checkout of your
own instead, check out [jaab-tech/fluxrig](https://github.com/jaab-tech/fluxrig)
next to this repository, at `../fluxrig`, and add a `replace` directive for it
in `go.mod`. If the engine lives elsewhere, point that directive there and set
`FLUXRIG_DIR` in `.env.local` (see `.env.local.example`).

| Command | What it does |
|:---|:---|
| `make test` | Runs the unit tests with the race detector |
| `make check-catalog` | Fails unless `fluxrig-ent` lists these gears and the open-source Rack lists none of them |
| `make test-e2e` | Runs the e2e tests, which are shell scripts in `test/e2e/`: a real Mixer and Rack with these gears, checked end to end |
| `make test-robot` | Runs the Robot suites in `test/robot/`, which check what the gears do in detail: values, rules, rate shapes |

The two kinds of test differ in depth, as they do in the engine repository. An e2e test
proves that a path works. A Robot suite proves what the gear does along it. Both cover
`sim_source` and `sim_responder`, and nothing else: the engine's own regression belongs
to the engine repository. Both run on the engine's test tooling, so the engine checkout
needs its tools installed.

## Use in your own binary

```go
import _ "github.com/jaab-tech/fluxrig-ent/pkg/gears"
```

A blank import registers both gears with the engine's gear factory. Put it in a
binary that calls `commands.Execute()` from the engine, as `cmd/fluxrig-ent` does.

## Versioning

Independent line: `v0.1.x` here pairs with a fluxrig release, it does not
mirror it. v0.1.0 shipped alongside fluxrig v0.11.0. Each version carries its
own BSL Change Date (four years after it ships); see [LICENSE](LICENSE) and
[CHANGELOG.md](CHANGELOG.md).

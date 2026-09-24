# fluxrig-ent: the enterprise gears and the binaries that carry them.
#
# The open-source engine does not import this module. Each binary built here is
# the engine's own entry point plus a blank import that registers the enterprise
# gears, so the open-source repository has no build target, tag or dependency
# that names them.

# Local settings, never committed. See .env.local.example.
-include .env.local

# The open-source repository, checked out next to this one. The replace directive
# in go.mod names ../fluxrig too: if it lives elsewhere, change both.
FLUXRIG_DIR ?= ../fluxrig
FLUXRIG_ABS := $(abspath $(FLUXRIG_DIR))

# Where the gear reference docs live, for `make gear-docs`. Same variable name
# and same target directory as the engine's own .env.local, since both write
# into fluxrig.org/docs/reference/gears; unset, the target skips silently.
GEAR_DOCS_DIR ?=

ENGINE          := github.com/jaab-tech/fluxrig
FLUXRIG_VERSION := $(shell cat $(FLUXRIG_DIR)/VERSION 2>/dev/null)
ENT_COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
FLUXRIG_COMMIT  := $(shell git -C $(FLUXRIG_DIR) rev-parse --short HEAD 2>/dev/null || echo none)
DATE            := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
ifneq ($(shell git status --porcelain 2>/dev/null)$(shell git -C $(FLUXRIG_DIR) status --porcelain 2>/dev/null),)
	DIRTY := -dirty
endif

# The version is the engine's, because that is what a Mixer and a Rack report to
# each other. The commit names both repositories the binary was built from.
LDFLAGS := -w -s \
	-X '$(ENGINE)/pkg/version.Version=$(FLUXRIG_VERSION)' \
	-X '$(ENGINE)/pkg/version.Commit=$(FLUXRIG_COMMIT)+ent.$(ENT_COMMIT)' \
	-X '$(ENGINE)/pkg/version.BuildDate=$(DATE)' \
	-X '$(ENGINE)/pkg/version.Dirty=$(DIRTY)'

# A binary links this repository and most of the engine, so a change in either
# must rebuild it. A stale binary makes a test exercise the old code silently.
ENT_SRC    := $(shell find cmd pkg -name '*.go')
ENGINE_SRC := $(shell find $(FLUXRIG_DIR)/cmd $(FLUXRIG_DIR)/pkg -name '*.go' -not -path '*/mixer/api/docs/*' 2>/dev/null)

# Where engine-names puts the enterprise builds under the engine's names.
ENGINE_NAMES := $(CURDIR)/bin/engine-names

.PHONY: all build vet test check-catalog engine-names test-e2e test-robot gear-docs clean help

all: vet test build

bin/fluxrig-ent: $(ENT_SRC) $(ENGINE_SRC) go.mod go.sum
	@echo "Building fluxrig-ent..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/fluxrig-ent ./cmd/fluxrig-ent

bin/fluxrig-mixer-ent: $(ENT_SRC) $(ENGINE_SRC) go.mod go.sum
	@echo "Building fluxrig-mixer-ent..."
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/fluxrig-mixer-ent ./cmd/fluxrig-mixer-ent

build: bin/fluxrig-ent bin/fluxrig-mixer-ent ## Build the Rack and the Mixer that carry the enterprise gears

# sim_source and sim_responder register into the engine's own gear factory
# (gears.RegisterExtension), so `fluxrig-ent gears doc` renders their manifests
# exactly like a native gear's, AUTOGEN markers and all. This is the same
# command and the same target directory as the engine's own `make gear-docs`,
# just run from this binary instead, since bin/fluxrig has no idea these gears
# exist.
gear-docs: bin/fluxrig-ent ## Refresh AUTOGEN manifest sections for sim_source and sim_responder
	@if [ -z "$(GEAR_DOCS_DIR)" ]; then \
		echo "Skipping gear docs (GEAR_DOCS_DIR not set, configure .env.local)"; \
	else \
		echo "Refreshing gear manifest docs in $(GEAR_DOCS_DIR)..."; \
		./bin/fluxrig-ent gears doc --write "$(GEAR_DOCS_DIR)"; \
	fi

# The licence-issuing tool (fluxrig-license) lives outside this repository,
# which is public: an internal issuing tool, or the habit of building one from
# this Makefile, has no business riding along here. It imports pkg/license
# from this module as its only dependency on fluxrig-ent.

vet: ## Run go vet
	go vet ./...

test: ## Run the unit tests with the race detector
	go test -race -count=1 ./...

# Two claims, both about what a binary can construct: the enterprise Rack lists the
# enterprise gears next to the engine's own, and the open-source Rack, when it has
# been built, lists none of them. Listing is cheap and needs no traffic.
check-catalog: bin/fluxrig-ent ## Fail unless the enterprise Rack lists the enterprise gears and the open-source one does not
	@types="$$(./bin/fluxrig-ent gears list)"; \
	for t in sim_source sim_responder io_tcp codec_iso8583; do \
		echo "$$types" | grep -q "^$$t " || { echo "check-catalog: $$t is missing from bin/fluxrig-ent"; exit 1; }; \
	done
	@if [ -x "$(FLUXRIG_DIR)/bin/fluxrig" ]; then \
		if "$(FLUXRIG_DIR)/bin/fluxrig" gears list | grep -qE '^sim_(source|responder) '; then \
			echo "check-catalog: the open-source binary lists enterprise gears"; exit 1; \
		fi; \
	fi
	@echo "check-catalog: bin/fluxrig-ent lists the enterprise gears and the engine's own; the open-source Rack lists none of them"

# The e2e tests and the Robot suites drive binaries by the engine's names. This
# directory maps those names onto the enterprise builds, and onto the engine's own
# iso8583-tool. The open-source Mixer is built too: the catalog test compares the
# two Mixers.
engine-names: build
	@GOMODCACHE="$$(go env GOMODCACHE)" $(MAKE) --no-print-directory -C $(FLUXRIG_DIR) bin/iso8583-tool bin/fluxrig-mixer
	@mkdir -p $(ENGINE_NAMES)
	@ln -sfn $(CURDIR)/bin/fluxrig-ent $(ENGINE_NAMES)/fluxrig
	@ln -sfn $(CURDIR)/bin/fluxrig-mixer-ent $(ENGINE_NAMES)/fluxrig-mixer
	@ln -sfn $(FLUXRIG_ABS)/bin/iso8583-tool $(ENGINE_NAMES)/iso8583-tool

# The two kinds of test cover the enterprise gears and nothing else: the engine's
# own regression belongs to the open-source repository. Both run on the engine's
# tooling, which needs two things from here: where the binaries are
# (FLUXRIG_BIN_DIR) and where the engine checkout is (FLUXRIG_DIR).

# e2e tests are shell scripts, like the engine's test/e2e. Each starts a real Mixer
# and Rack with the enterprise gears and checks the path end to end.
test-e2e: check-catalog engine-names ## Run the e2e tests (shell) for the enterprise gears
	@GOMODCACHE="$$(go env GOMODCACHE)" $(MAKE) --no-print-directory -C $(FLUXRIG_DIR) robot-prep
	FLUXRIG_BIN_DIR=$(ENGINE_NAMES) FLUXRIG_DIR=$(FLUXRIG_ABS) $(CURDIR)/test/e2e/run_all.sh

# Robot suites check what the gears do in detail: values, rules, rate shapes. They run
# on the engine's Robot runner.
test-robot: check-catalog engine-names ## Run the Robot suites for the enterprise gears
	@GOMODCACHE="$$(go env GOMODCACHE)" $(MAKE) --no-print-directory -C $(FLUXRIG_DIR) robot-prep
	FLUXRIG_BIN_DIR=$(ENGINE_NAMES) FLUXRIG_DIR=$(FLUXRIG_ABS) \
		$(FLUXRIG_ABS)/test/robot/run.sh $(CURDIR)/test/robot/suites/simulator

clean: ## Remove the built binaries
	rm -rf bin

help: ## Display this help screen
	@grep -h -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'

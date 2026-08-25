GO ?= go
NPM ?= npm
BINARY := bin/codehelper
MODULE := github.com/fwtllh-png/CodeHelper
START_WORKSPACE ?= $(CURDIR)
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
INSTALL_BINARY := $(BINDIR)/codehelper
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.Date=$(BUILD_DATE)

.PHONY: start install uninstall fmt verify test test-hermetic test-platform-capability reliability-gate test-integration \
	test-release release-baseline-check integration-gate release-gate race build cross-build smoke \
	agent-preflight agent-ratchet-test ratchet-fast \
	docs-check book-check web-experience-check \
	host-journey-contract \
	benchmark-v2-check benchmark-v2 hotspot-baseline architecture-metrics \
	observation-traits observation-traits-check \
	web-protocol web-protocol-check \
	provider-deepseek-live-control provider-deepseek-live-ce7 \
	architecture-ratchet architecture-freeze \
	book-navigation \
	turn-kernel-convergence-baseline turn-kernel-convergence-exit-gate \
	doc-governance-check doc-governance-test doc-impact \
	doc-reverify doc-reverify-dry-run \
	doc-external-links release-fact-check brand-check \
	security-test sandbox-attack-test secret-leak-test \
	stress stress-nightly \
	canary canary-nightly \
		canary-adversarial canary-adversarial-quick \
	web-host-smoke protocol-contract protocol-schema \
	web-install web-check web-test web-compile web-measure web-build web-brand-assets web-assets-check web-e2e web-parity-check web-parity-report \
		web-harness-parity-check \
		web-release-drill web-streaming-soak web-performance web-supply-chain-check web-vulnerability-check \
	deepseek-init deepseek-web \
	bench catalog-bench package clean

# Stress tests run concurrent pressure tests to catch deadlocks,
# goroutine leaks, and channel blocking under high concurrency.
# Run with: make stress
stress:
	$(GO) test -tags=stress -race -count=1 -timeout 5m -run '^TestStress' \
		./internal/runtime/agent/engine/... \
		./internal/adapter/mcp/... \
		./internal/runtime/app/eventhub/...

# stress-nightly runs extended stress tests for nightly CI.
stress-nightly:
	$(GO) test -tags=stress -race -count=3 -timeout 15m -run '^TestStress' \
		./internal/runtime/agent/engine/... \
		./internal/adapter/mcp/... \
		./internal/runtime/app/eventhub/...

# canary runs behavioral replay and performance regression checks against
# the hermetic benchmark suite. Fails on any regression.
# Run after: make build
# Baseline: scripts/canary-replay.py record && scripts/canary-perf.py baseline
canary:
	python3 scripts/canary-replay.py check --report .tmp/canary-shared-report.json
	python3 scripts/canary-perf.py check --report .tmp/canary-shared-report.json --reuse-report

# canary-nightly runs extended canary checks with tighter thresholds for
# nightly CI. Uses a lower performance regression threshold (20% vs 30%).
canary-nightly:
	python3 scripts/canary-replay.py check --report .tmp/canary-shared-report.json
	CANARY_PERF_THRESHOLD=0.20 python3 scripts/canary-perf.py check \
		--report .tmp/canary-shared-report.json --reuse-report

# canary-adversarial runs active bug-finding tests: fault injection,
# differential config, and fixture mutation. Use this to discover
# real bugs, not just regressions.
canary-adversarial:
	python3 scripts/canary-adversarial.py full

# canary-adversarial-quick runs a faster subset of adversarial tests
# suitable for pre-commit or PR checks.
canary-adversarial-quick:
	python3 scripts/canary-adversarial.py fault-inject --timeout 60

PROTOCOL_SCHEMA := docs/protocol/runtime-protocol.schema.json
WEB_HOST_CONTRACT := docs/protocol/web-host.contract.json
WEB_HOST_TYPES := web/src/protocol/web-host.generated.ts
WEB_STREAMING_SOAK_DURATION ?= 1h
WEB_STREAMING_SOAK_TIMEOUT ?= 70m
WEB_STREAMING_SOAK_ALLOW_SHORT ?= 0
ARCHITECTURE_METRICS_BASELINE := testdata/contracts/architecture-metrics-baseline.json
RELIABILITY_MATRIX := testdata/contracts/reliability-matrix.json
ARCHITECTURE_METRICS_REPORT ?= .tmp/architecture/metrics.json
AGENT_PREFLIGHT_DIR ?= .tmp/agent-preflight
AGENT_PREFLIGHT_SNAPSHOT ?= $(AGENT_PREFLIGHT_DIR)/baseline.json
AGENT_PREFLIGHT_ARCHITECTURE ?= $(AGENT_PREFLIGHT_DIR)/architecture.json
AGENT_PREFLIGHT_WEB ?= $(AGENT_PREFLIGHT_DIR)/web.json
AGENT_CURRENT_ARCHITECTURE ?= $(AGENT_PREFLIGHT_DIR)/current-architecture.json
AGENT_CURRENT_WEB ?= $(AGENT_PREFLIGHT_DIR)/current-web.json
WEB_MEASUREMENT_REPORT ?= .tmp/web-supply-chain-report.json
ARCHITECTURE_BASE_REF ?= origin/main
ARCHITECTURE_BASELINE_BASE_PATH ?= $(shell \
	if git cat-file -e '$(ARCHITECTURE_BASE_REF):$(ARCHITECTURE_METRICS_BASELINE)' 2>/dev/null; then \
		printf '%s' '$(ARCHITECTURE_METRICS_BASELINE)'; \
	else \
		printf '%s' 'docs/architecture-metrics-baseline.json'; \
	fi)
BASE_REF ?= $(ARCHITECTURE_BASE_REF)

RELEASE_STAGE ?= experimental
PREVIOUS_RELEASE_REF ?=
PREVIOUS_BINARY ?=
TEST_LANE_REPORT_DIR ?= .tmp/test-lanes
TEST_PACKAGE_PARALLELISM ?= 1
TEST_HOME ?= $(CURDIR)/.tmp/test-home
TEST_GOPATH ?= $(shell $(GO) env GOPATH)
TEST_GOMODCACHE ?= $(shell $(GO) env GOMODCACHE)
TEST_GOCACHE ?= $(shell $(GO) env GOCACHE)
TEST_HOME_ENV := HOME='$(TEST_HOME)' GOPATH='$(TEST_GOPATH)' \
	GOMODCACHE='$(TEST_GOMODCACHE)' GOCACHE='$(TEST_GOCACHE)'
PLATFORM_CAPABILITY_ARGS := --available-on darwin --available-on linux

ifeq ($(shell uname -s 2>/dev/null),Darwin)
PLATFORM_CAPABILITY_ARGS += --requires-command sandbox-exec
else ifeq ($(shell uname -s 2>/dev/null),Linux)
PLATFORM_CAPABILITY_ARGS += --requires-command bwrap
endif

fmt:
	$(GO) fmt ./...

agent-ratchet-test:
	node --test scripts/agent-ratchet.test.mjs

agent-preflight:
	@mkdir -p '$(AGENT_PREFLIGHT_DIR)'
	@rm -f '$(AGENT_PREFLIGHT_ARCHITECTURE)' '$(AGENT_PREFLIGHT_WEB)'
	@$(GO) run ./scripts/architecturemetrics -root . \
		-baseline '$(ARCHITECTURE_METRICS_BASELINE)' \
		-report '$(AGENT_PREFLIGHT_ARCHITECTURE)' \
		>'$(AGENT_PREFLIGHT_DIR)/architecture.log' 2>&1 || \
		test -s '$(AGENT_PREFLIGHT_ARCHITECTURE)'
	@$(MAKE) --no-print-directory web-measure \
		WEB_MEASUREMENT_REPORT='$(AGENT_PREFLIGHT_WEB)' \
		>'$(AGENT_PREFLIGHT_DIR)/web.log' 2>&1 || { \
			cat '$(AGENT_PREFLIGHT_DIR)/web.log'; exit 1; \
		}
	@node scripts/agent-ratchet.mjs snapshot \
		--architecture-report '$(AGENT_PREFLIGHT_ARCHITECTURE)' \
		--web-report '$(AGENT_PREFLIGHT_WEB)' \
		--architecture-policy '$(ARCHITECTURE_METRICS_BASELINE)' \
		--web-policy testdata/contracts/web-supply-chain-policy.json \
		--output '$(AGENT_PREFLIGHT_SNAPSHOT)'

ratchet-fast:
	@test -s '$(AGENT_PREFLIGHT_SNAPSHOT)' || { \
		printf '%s\n' 'agent preflight is missing; run make agent-preflight first'; \
		exit 1; \
	}
	@rm -f '$(AGENT_CURRENT_ARCHITECTURE)' '$(AGENT_CURRENT_WEB)'
	@$(GO) run ./scripts/architecturemetrics -root . \
		-baseline '$(ARCHITECTURE_METRICS_BASELINE)' \
		-report '$(AGENT_CURRENT_ARCHITECTURE)' \
		>'$(AGENT_PREFLIGHT_DIR)/current-architecture.log' 2>&1 || \
		test -s '$(AGENT_CURRENT_ARCHITECTURE)'
	@$(MAKE) --no-print-directory web-measure \
		WEB_MEASUREMENT_REPORT='$(AGENT_CURRENT_WEB)' \
		>'$(AGENT_PREFLIGHT_DIR)/current-web.log' 2>&1 || { \
			cat '$(AGENT_PREFLIGHT_DIR)/current-web.log'; exit 1; \
		}
	@node scripts/agent-ratchet.mjs check \
		--snapshot '$(AGENT_PREFLIGHT_SNAPSHOT)' \
		--architecture-report '$(AGENT_CURRENT_ARCHITECTURE)' \
		--web-report '$(AGENT_CURRENT_WEB)' \
		--architecture-policy '$(ARCHITECTURE_METRICS_BASELINE)' \
		--web-policy testdata/contracts/web-supply-chain-policy.json

verify: architecture-ratchet docs-check book-check brand-check web-protocol-check web-parity-check \
	web-check web-test web-assets-check web-supply-chain-check \
	reliability-gate
	@unformatted="$$(git ls-files --cached --others --exclude-standard '*.go' | \
		while IFS= read -r file; do \
			test ! -f "$$file" || gofmt -l "$$file"; \
		done)"; \
		test -z "$$unformatted" || { \
			echo "gofmt required:"; printf '%s\n' "$$unformatted"; exit 1; \
		}
	$(GO) vet ./...
	$(MAKE) test-hermetic
	$(GO) test -race -p 1 ./...

test: test-hermetic

hotspot-baseline:
	$(GO) test -count=1 ./scripts -run 'Test(RepositoryHotspotBaseline|CheckHotspot)'
	$(GO) run ./scripts/check-hotspot-baseline.go -root .

architecture-metrics:
	$(GO) test -count=1 ./scripts/architecturemetrics
	$(GO) run ./scripts/architecturemetrics -root . \
		-baseline '$(ARCHITECTURE_METRICS_BASELINE)' \
		-report '$(ARCHITECTURE_METRICS_REPORT)'

architecture-ratchet: architecture-metrics
	@if test -n '$(ARCHITECTURE_BASE_REF)' && \
		git cat-file -e '$(ARCHITECTURE_BASE_REF):$(ARCHITECTURE_BASELINE_BASE_PATH)' 2>/dev/null; then \
		$(GO) run ./scripts/architecturemetrics -root . \
			-baseline '$(ARCHITECTURE_METRICS_BASELINE)' \
			-base-ref '$(ARCHITECTURE_BASE_REF)' \
			-base-baseline '$(ARCHITECTURE_BASELINE_BASE_PATH)'; \
	else \
		printf '%s\n' 'architecture ratchet comparison skipped: base baseline is unavailable'; \
	fi

provider-deepseek-live-control:
	CODEHELPER_DEEPSEEK_LIVE_CONTROL=1 \
		$(GO) test -count=1 -v ./internal/adapter/provider/httpclient \
		-run '^TestDeepSeekP0LiveControl$$'

provider-deepseek-live-ce7:
	CODEHELPER_DEEPSEEK_LIVE_CONTROL=1 \
		$(GO) test -count=1 -v ./internal/adapter/provider/httpclient \
		-run '^TestDeepSeek(P0LiveControl|CE7LiveCacheShare)$$'

# Architecture behavior freeze. Package tests carry characterization, config
# provenance drift, state transitions, and schema drift. Race is focused on the
# concurrent turn engine.
architecture-freeze: hotspot-baseline architecture-ratchet
	@mkdir -p '$(TEST_HOME)'
	$(TEST_HOME_ENV) $(GO) test -count=1 \
		./internal/runtime/agent/engine \
		./internal/config \
		./internal/runtime/protocol
	$(TEST_HOME_ENV) $(GO) test -race -count=1 \
		./internal/runtime/agent/engine

# Hermetic is the default developer lane: no network, live credentials, GUI,
# or host sandbox capability. Serial package execution avoids resource flakes.
test-hermetic:
	@mkdir -p '$(TEST_HOME)'
	python3 scripts/run-test-lane.py hermetic \
		--report '$(TEST_LANE_REPORT_DIR)/hermetic.json' \
		-- env $(TEST_HOME_ENV) $(GO) test -count=1 \
			-p '$(TEST_PACKAGE_PARALLELISM)' ./...

# Capability tests are compiled only in this lane. Missing host prerequisites
# produce an explicit unavailable report; CI sets CAPABILITY_REQUIRED.
test-platform-capability:
	CODEHELPER_SANDBOX_STAGE=1 python3 scripts/run-test-lane.py platform-capability \
		--report '$(TEST_LANE_REPORT_DIR)/platform-capability.json' \
		--unavailable-pattern sandbox_unavailable \
		$(PLATFORM_CAPABILITY_ARGS) $(CAPABILITY_REQUIRED) \
		-- $(GO) test -tags=capability -count=1 \
			./internal/security/sandbox/... ./internal/platform/process/...

reliability-gate: observation-traits-check
	python3 scripts/check-reliability-matrix.py \
		'$(RELIABILITY_MATRIX)' --run

test-integration:
	python3 scripts/run-test-lane.py integration \
		--report '$(TEST_LANE_REPORT_DIR)/integration.json' \
		--requires-command go --requires-command npm \
		$(INTEGRATION_REQUIRED) \
		-- $(MAKE) integration-gate

integration-gate: build web-build
	$(GO) test -count=1 ./internal/host/runtimeapi/web ./internal/host/web

test-release: release-baseline-check
	python3 scripts/run-test-lane.py release \
		--report '$(TEST_LANE_REPORT_DIR)/release.json' \
		--requires-command go --requires-command npm \
		--unavailable-pattern sandbox_unavailable \
		--require-available \
		-- $(MAKE) release-gate

release-baseline-check:
	@if test -n '$(PREVIOUS_BINARY)'; then \
		test -x '$(PREVIOUS_BINARY)' || { \
			echo "PREVIOUS_BINARY is not executable: $(PREVIOUS_BINARY)" >&2; \
			exit 2; \
		}; \
	else \
		./scripts/validate-release-ref.sh '$(PREVIOUS_RELEASE_REF)' >/dev/null; \
	fi

release-gate: cross-build smoke race secret-leak-test reliability-gate benchmark-v2 web-performance \
	web-streaming-soak web-parity-report web-release-drill web-supply-chain-check web-vulnerability-check
	@dirty="$$(git status --porcelain --untracked-files=all)"; \
		test -z "$$dirty" || { \
			echo "release gate requires a clean worktree:"; \
			printf '%s\n' "$$dirty"; \
			exit 1; \
		}

race:
	$(GO) test -race -p 1 ./...

build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/codehelper

start:
	$(MAKE) web-install
	$(MAKE) web-build
	$(MAKE) build
	./$(BINARY) --workspace '$(START_WORKSPACE)' --enable-tools --posture suggest --open

install:
	$(MAKE) web-install
	$(MAKE) web-build
	$(MAKE) build
	@mkdir -p '$(BINDIR)'
	@tmp="$$(mktemp '$(BINDIR)/.codehelper.XXXXXX')"; \
		trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
		cp '$(BINARY)' "$$tmp"; \
		chmod 0755 "$$tmp"; \
		mv -f "$$tmp" '$(INSTALL_BINARY)'
	@printf 'Installed CodeHelper: %s\n' '$(INSTALL_BINARY)'
	@if command -v codehelper >/dev/null 2>&1; then \
		printf 'Run from any workspace: codehelper\n'; \
	else \
		printf 'Add this directory to PATH: export PATH="%s:$$PATH"\n' '$(BINDIR)'; \
	fi

uninstall:
	@rm -f '$(INSTALL_BINARY)'
	@printf 'Removed CodeHelper: %s\n' '$(INSTALL_BINARY)'

web-install:
	$(NPM) --prefix web ci

web-check:
	$(NPM) --prefix web run check

web-test:
	$(NPM) --prefix web test

web-performance:
	$(NPM) --prefix web test -- --run src/ui/performance.test.ts

web-supply-chain-check: web-build

web-vulnerability-check:
	@mkdir -p .tmp
	@$(NPM) --prefix web audit --audit-level=high --json > .tmp/web-npm-audit.json || { \
		cat .tmp/web-npm-audit.json; \
		exit 1; \
	}

web-compile:
	$(NPM) --prefix web run build
	$(GO) run ./scripts/webassetmanifest -dist web/dist -output web/dist/asset-manifest.json

web-measure: web-compile
	node scripts/web-supply-chain-check.mjs . --measure-only \
		--report '$(WEB_MEASUREMENT_REPORT)'

web-build: web-compile
	node scripts/web-supply-chain-check.mjs .

web-brand-assets:
	$(NPM) --prefix web run brand-assets

web-assets-check: web-brand-assets
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	$(NPM) --prefix web run build -- --outDir "$$tmp/dist" >/dev/null; \
	$(GO) run ./scripts/webassetmanifest \
		-dist "$$tmp/dist" -output "$$tmp/dist/asset-manifest.json"; \
	diff -ru web/dist "$$tmp/dist"

web-e2e: web-assets-check build
	$(NPM) --prefix web run test:e2e

web-parity-check: web-harness-parity-check
	$(GO) run ./scripts/webparitycheck -root . -mode check

web-harness-parity-check:
	$(GO) run ./scripts/webharnessparity

web-parity-report:
	$(GO) run ./scripts/webparitycheck -root . -mode report

web-release-drill: build
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	previous='$(PREVIOUS_BINARY)'; \
	if test -z "$$previous"; then \
		test -n '$(PREVIOUS_RELEASE_REF)' || { \
			echo "PREVIOUS_RELEASE_REF or PREVIOUS_BINARY is required" >&2; \
			exit 2; \
		}; \
		./scripts/validate-release-ref.sh '$(PREVIOUS_RELEASE_REF)' >/dev/null; \
		git archive '$(PREVIOUS_RELEASE_REF)' | tar -x -C "$$tmp"; \
		(cd "$$tmp" && $(GO) build -trimpath -o "$$tmp/codehelper-previous" ./cmd/codehelper); \
		previous="$$tmp/codehelper-previous"; \
	fi; \
	python3 scripts/web-release-drill.py \
		--current-binary '$(CURDIR)/$(BINARY)' \
		--previous-binary "$$previous" \
		--workspace '$(CURDIR)' \
		--fixture '$(CURDIR)/testdata/providers/openai' \
		--report '$(CURDIR)/.tmp/release/web-downgrade-drill.json'

web-streaming-soak:
	CODEHELPER_WEB_STREAMING_SOAK_DURATION=$(WEB_STREAMING_SOAK_DURATION) \
		CODEHELPER_WEB_STREAMING_SOAK_ALLOW_SHORT=$(WEB_STREAMING_SOAK_ALLOW_SHORT) \
		$(GO) test -count=1 -timeout $(WEB_STREAMING_SOAK_TIMEOUT) \
		-run '^TestWebSocketSustainedStreamingSoak$$' \
		./internal/host/runtimeapi/web

cross-build:
	@tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -o "$$tmp/codehelper-linux-amd64" ./cmd/codehelper; \
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o "$$tmp/codehelper-linux-arm64" ./cmd/codehelper; \
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -o "$$tmp/codehelper-windows-amd64.exe" ./cmd/codehelper

smoke: build
	./$(BINARY) --help >/dev/null
	./$(BINARY) --version

docs-check: web-experience-check benchmark-v2-check
	./scripts/check-docs.sh
	$(MAKE) doc-governance-check
	$(MAKE) doc-governance-test

book-check:
	./scripts/check-book.sh

book-navigation:
	python3 scripts/render-book-navigation.py

turn-kernel-convergence-baseline:
	$(GO) test -count=1 \
		./internal/runtime/agent/turnkernel \
		./internal/runtime/agent/engine \
		./internal/runtime/app \
		./internal/runtime/app/wire \
		./internal/persist/state/sqlite \
		./internal/persist/state/turnstate \
		-run 'Test(C0|C1|C2|C3|C4|C5|C6|Phase4R)'

# Final production ownership gate.
turn-kernel-convergence-exit-gate:
	CODEHELPER_TURN_KERNEL_CONVERGENCE_EXIT_GATE=1 $(GO) test -count=1 \
		./internal/runtime/agent/turnkernel \
		./internal/runtime/app \
		-run '^TestC0.*ExitGate$$'

web-experience-check:
	$(GO) run ./scripts/webexperiencecheck

host-journey-contract:
	$(GO) test -count=1 ./internal/host/runtimeapi/runtimecontract
	$(GO) test -count=1 ./internal/host/runtimeapi/web
	$(GO) test -count=1 ./internal/host/web
	$(NPM) --prefix web test

doc-governance-check:
	python3 scripts/check-doc-governance.py check

doc-governance-test:
	python3 -m unittest discover -s scripts/tests -p 'test_*.py'

doc-reverify:
	python3 scripts/check-doc-governance.py reverify

doc-reverify-dry-run:
	python3 scripts/check-doc-governance.py reverify --dry-run

doc-impact:
	@test -n "$(BASE_REF)" || { echo "BASE_REF is required" >&2; exit 2; }
	python3 scripts/check-doc-governance.py impact --base "$(BASE_REF)" --head "$${HEAD_REF:-HEAD}"

doc-external-links:
	python3 scripts/check-doc-governance.py external-links

release-fact-check:
	python3 scripts/check-doc-governance.py release

brand-check:
	./scripts/check-brand.sh
	./scripts/test-brand-check.sh

security-test:
	$(GO) test -race ./internal/security/... ./internal/adapter/tool/guard/... ./internal/adapter/plugin/... ./internal/host/web/... ./internal/runtime/agent/engine/... ./internal/runtime/app/...
	$(GO) test -race ./internal/platform/process/... -run 'Test(RunUsesInjectedStrongSandboxBackend|RunFailsClosedWithoutStrongSandbox|RunSanitizesRegularAndPTYEnvironments|RunPinsWorkingDirectoryToDescriptor|SanitizedEnvironment)'

sandbox-attack-test:
	CODEHELPER_SANDBOX_STAGE=1 $(GO) test -tags=capability -race \
		./internal/security/sandbox/... ./internal/adapter/tool/file/... ./internal/adapter/tool/shell/...
	CODEHELPER_SANDBOX_STAGE=1 $(GO) test -tags=capability -race \
		./internal/platform/process/... \
		-run 'Test(RunUsesInjectedStrongSandboxBackend|RunFailsClosedWithoutStrongSandbox|RunPinsWorkingDirectoryToDescriptor|SessionCancellationKillsProcessGroup|RealSandboxAttackCorpus|RealManagedProxyBlocksDirectEgress)'

secret-leak-test: build
	$(GO) test -race ./internal/config ./internal/observability/telemetry \
		./internal/platform/process/... \
		-run 'Test(Secret|JSONLogger|RunSanitizesRegularAndPTYEnvironments|SanitizedEnvironment)'

web-host-smoke:
	$(GO) test -race -count=1 ./internal/host/web/...

protocol-contract:
	$(GO) test -count=1 -v ./internal/runtime/app/... ./internal/host/runtimeapi/web/...

# protocol-schema regenerates the published protocol shapes. The drift test in
# internal/runtime/protocol fails when the committed copy is stale.
protocol-schema:
	$(GO) run ./scripts/eventtraitgen ./internal/runtime/protocol/event_traits.json ./internal/runtime/protocol/event_traits.gen.go
	$(GO) run ./internal/runtime/protocol/schemagen $(PROTOCOL_SCHEMA)
	$(GO) run ./scripts/webprotocolgen -output $(WEB_HOST_CONTRACT) -typescript $(WEB_HOST_TYPES)

web-protocol:
	$(GO) run ./scripts/webprotocolgen -output $(WEB_HOST_CONTRACT) -typescript $(WEB_HOST_TYPES)

web-protocol-check:
	$(GO) run ./scripts/webprotocolgen -output $(WEB_HOST_CONTRACT) -typescript $(WEB_HOST_TYPES) -check

observation-traits:
	$(GO) run ./scripts/observationtraitgen \
		-manifest internal/observability/schema/observation_traits.json \
		-go internal/observability/observation/traits.gen.go \
		-typescript web/src/protocol/observation.generated.ts \
		-schema docs/protocol/observation.schema.json

observation-traits-check:
	$(GO) run ./scripts/observationtraitgen \
		-manifest internal/observability/schema/observation_traits.json \
		-go internal/observability/observation/traits.gen.go \
		-typescript web/src/protocol/observation.generated.ts \
		-schema docs/protocol/observation.schema.json \
		-check

deepseek-init:
	./scripts/deepseek-local.sh init

deepseek-web:
	./scripts/deepseek-local.sh web

# bench runs the hermetic coding benchmark (fixture provider, no network/model).
# Set BENCH_REPORT to write the JSON report for tracking across runs.
bench:
	CODEHELPER_BENCH_REPORT='$(BENCH_REPORT)' $(GO) test -tags=capability \
		-count=1 -v ./internal/host/bench/...

benchmark-v2-check:
	$(GO) test -count=1 ./scripts/benchmarkv2
	$(GO) run ./scripts/benchmarkv2 -root .

benchmark-v2: benchmark-v2-check bench
	$(GO) test -count=1 -run 'Recovery' ./internal/persist/workspacejournal
	$(GO) test -count=1 -run '^TestWebSocketDownlinkConcurrencyAndShutdown$$' \
		./internal/host/runtimeapi/web
	$(GO) test -count=1 -run '^TestRunContextStartsAndStopsWebHost$$' \
		./internal/host/web
	$(GO) test -count=1 -run '^TestWeb(Socket(ReplaysTenThousandEvents|CapsBrowserConnectionsAtSixteen|DisconnectStormReleasesSlotsGoroutinesAndDescriptors)|SessionCapacity(AllowsThirtyTwoAndPreservesIdempotentRetry|IsAtomicUnderConcurrentCreate))$$' \
		./internal/host/runtimeapi/web
	$(NPM) --prefix web run test:e2e -- visual.spec.ts --grep 'reloads|frozen'
	$(NPM) --prefix web test -- --testNamePattern \
		'windows 500-turn transcripts to 200 projected rows with older and newer navigation'

# catalog-bench tracks the M4 dynamic tool catalog's time, allocation, and
# prompt-size baseline at 100/500/1000 tools.
catalog-bench:
	$(GO) test -run '^$$' \
		-bench 'BenchmarkTool(Catalog|RegistryStartup)Scale' \
		-benchtime=10x -benchmem ./internal/runtime/agent/prompt

package: web-assets-check build
	VERSION='$(VERSION)' RELEASE_STAGE='$(RELEASE_STAGE)' ./scripts/package-release.sh

clean:
	rm -rf bin dist .tmp .dbg web/dist

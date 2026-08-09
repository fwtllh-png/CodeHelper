GO ?= go
NPM ?= npm
BINARY := bin/codehelper
MODULE := github.com/fwtllh-png/CodeHelper
VSCODE_DIR := extensions/vscode
VSCODE_CLI ?= /Applications/Visual Studio Code.app/Contents/Resources/app/bin/code
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
	-X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(MODULE)/internal/buildinfo.Date=$(BUILD_DATE)

.PHONY: fmt verify test test-hermetic test-platform-capability test-integration \
	test-release integration-gate release-gate race build cross-build smoke \
	docs-check book-check experience-check experience-baseline \
	experience-electron-baseline host-journey-contract \
	benchmark-v2-check benchmark-v2 hotspot-baseline architecture-freeze \
	book-navigation command-docs command-docs-check \
	doc-governance-check doc-governance-test doc-impact \
	doc-external-links release-fact-check brand-check \
	markdownlint-check \
	security-test sandbox-attack-test secret-leak-test live-model-smoke \
	cli-smoke tui-smoke acp-interop protocol-contract protocol-schema \
	vscode-install vscode-protocol-check vscode-compatibility vscode-check vscode-test \
	vscode-security vscode-performance vscode-runtime-integration \
	vscode-integration vscode-rosetta-integration \
	vscode-build vscode-package vscode-release-dry-run \
	vscode-multiroot-integration vscode-update-integration \
	vscode-distribution vscode-local-setup vscode-matrix-report vscode-rc \
	deepseek-init deepseek-tui deepseek-vscode \
	bench upgrade-baseline catalog-bench package clean

PROTOCOL_SCHEMA := docs/protocol/runtime-protocol.schema.json

FUZZTIME ?= 30s
RELEASE_STAGE ?= experimental
TEST_LANE_REPORT_DIR ?= .tmp/test-lanes
TEST_PACKAGE_PARALLELISM ?= 1
TEST_HOME ?= $(CURDIR)/.tmp/test-home
TEST_GOPATH ?= $(shell $(GO) env GOPATH)
TEST_GOMODCACHE ?= $(shell $(GO) env GOMODCACHE)
TEST_GOCACHE ?= $(shell $(GO) env GOCACHE)
UPGRADE_BASELINE_REPORT ?= docs/upgrade-baseline.json
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

verify: markdownlint-check docs-check book-check brand-check vscode-check vscode-test
	@test -z "$$(gofmt -l .)" || { echo "gofmt required:"; gofmt -l .; exit 1; }
	$(GO) vet ./...
	$(MAKE) test-hermetic
	$(GO) test -race -p 1 ./...

test: test-hermetic

hotspot-baseline:
	$(GO) test -count=1 ./scripts -run 'Test(RepositoryHotspotBaseline|CheckHotspot)'
	$(GO) run ./scripts/check-hotspot-baseline.go -root .

# Architecture behavior freeze. Package tests carry characterization, visual/wire
# goldens, config provenance drift, state transitions, and schema drift. Race is
# focused on the concurrent TUI and turn engine.
architecture-freeze: hotspot-baseline
	@mkdir -p '$(TEST_HOME)'
	$(TEST_HOME_ENV) $(GO) test -count=1 \
		./internal/host/tui \
		./internal/runtime/agent/engine \
		./internal/config \
		./internal/runtime/protocol
	$(TEST_HOME_ENV) $(GO) test -race -count=1 \
		./internal/host/tui \
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

test-integration:
	python3 scripts/run-test-lane.py integration \
		--report '$(TEST_LANE_REPORT_DIR)/integration.json' \
		--requires-command go --requires-command npm \
		$(INTEGRATION_REQUIRED) \
		-- $(MAKE) integration-gate

integration-gate: build vscode-install
	$(MAKE) acp-interop
	$(MAKE) vscode-runtime-integration

test-release:
	python3 scripts/run-test-lane.py release \
		--report '$(TEST_LANE_REPORT_DIR)/release.json' \
		--requires-command go --requires-command npm \
		--unavailable-pattern sandbox_unavailable \
		--require-available \
		-- $(MAKE) release-gate

release-gate: cross-build smoke race secret-leak-test benchmark-v2 vscode-release-dry-run

race:
	$(GO) test -race -p 1 ./...

build:
	@mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/codehelper

cross-build:
	@tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -o "$$tmp/codehelper-linux-amd64" ./cmd/codehelper; \
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o "$$tmp/codehelper-linux-arm64" ./cmd/codehelper; \
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -o "$$tmp/codehelper-windows-amd64.exe" ./cmd/codehelper

smoke: build
	./$(BINARY) help >/dev/null
	./$(BINARY) version
	./$(BINARY) version --json

docs-check: command-docs-check experience-check benchmark-v2-check
	./scripts/check-docs.sh
	$(MAKE) doc-governance-check
	$(MAKE) doc-governance-test

markdownlint-check: vscode-install
	./extensions/vscode/node_modules/.bin/markdownlint-cli2

book-check:
	./scripts/check-book.sh

book-navigation:
	python3 scripts/render-book-navigation.py

command-docs:
	$(GO) run ./scripts/commanddocs

command-docs-check:
	$(GO) run ./scripts/commanddocs --check

experience-check:
	$(GO) run ./scripts/experiencecontract

experience-baseline: experience-check vscode-install
	$(GO) test ./internal/host/tui -run VisualSnapshot -count=1
	cd $(VSCODE_DIR) && $(NPM) run check
	cd $(VSCODE_DIR) && $(NPM) test -- experience

experience-electron-baseline: build vscode-install
	cd $(VSCODE_DIR) && \
		CODEHELPER_VSCODE_BINARY='$(CURDIR)/$(BINARY)' \
		CODEHELPER_VSCODE_SELECTION_FIXTURE='$(CURDIR)/testdata/providers/selection-commands' \
		CODEHELPER_ELECTRON_SCENARIOS=empty,workspace \
		$(NPM) run test:electron

host-journey-contract: vscode-install
	$(GO) test -count=1 ./internal/host/runtimeapi/acp -run MeetsTheProtocolContract
	$(GO) test -count=1 ./internal/host/cli -run Quickstart
	$(GO) test -count=1 ./internal/host/tui -run HostJourney
	cd $(VSCODE_DIR) && $(NPM) test -- experience

doc-governance-check:
	python3 scripts/check-doc-governance.py check

doc-governance-test:
	python3 -m unittest discover -s scripts/tests -p 'test_*.py'

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
	$(GO) test -race ./internal/security/... ./internal/adapter/tool/guard/... ./internal/adapter/plugin/... ./internal/host/cli/... ./internal/runtime/agent/engine/... ./internal/runtime/app/...
	$(GO) test -race ./internal/platform/process/... -run 'Test(RunUsesInjectedStrongSandboxBackend|RunFailsClosedWithoutStrongSandbox|RunSanitizesRegularAndPTYEnvironments|RunPinsWorkingDirectoryToDescriptor|SanitizedEnvironment)'

sandbox-attack-test:
	CODEHELPER_SANDBOX_STAGE=1 $(GO) test -tags=capability -race \
		./internal/security/sandbox/... ./internal/adapter/tool/file/... ./internal/adapter/tool/shell/...
	CODEHELPER_SANDBOX_STAGE=1 $(GO) test -tags=capability -race \
		./internal/platform/process/... \
		-run 'Test(RunUsesInjectedStrongSandboxBackend|RunFailsClosedWithoutStrongSandbox|RunPinsWorkingDirectoryToDescriptor|SessionCancellationKillsProcessGroup|RealSandboxAttackCorpus)'

secret-leak-test: build
	./scripts/test-secret-leak.sh ./$(BINARY)
	$(GO) test -race ./internal/platform/process/... -run 'Test(RunSanitizesRegularAndPTYEnvironments|SanitizedEnvironment)'

live-model-smoke: build
	./scripts/live-model-smoke.sh ./$(BINARY)

cli-smoke:
	$(GO) test -race -count=1 ./internal/host/cli/... -run 'Test(RunHelp|RunVersion|RunUnknown|RunMachine|Auth|Model|Thread|Doctor|Cobra)'

tui-smoke:
	$(GO) test -race -count=1 ./internal/host/tui/...

# acp-interop drives the release binary over real stdio. The tests skip
# themselves without CODEHELPER_ACP_BINARY, so they only run through this target.
acp-interop: build
	CODEHELPER_ACP_BINARY='$(CURDIR)/$(BINARY)' $(GO) test -count=1 -v \
		./internal/host/runtimeapi/acp/... -run TestBinaryInterop

# protocol-contract runs the shared runtime scenarios through the ACP host.
protocol-contract:
	$(GO) test -count=1 -v ./internal/host/runtimeapi/acp/... \
		-run 'MeetsTheProtocolContract'

# protocol-schema regenerates the published protocol shapes. The drift test in
# internal/runtime/protocol fails when the committed copy is stale.
protocol-schema:
	$(GO) run ./internal/runtime/protocol/schemagen $(PROTOCOL_SCHEMA)

# VS Code unit and type checks never download Electron. Runtime, Electron, and
# packaging gates stay separate from default verification.
vscode-install:
	cd $(VSCODE_DIR) && $(NPM) ci

vscode-protocol-check:
	$(GO) test ./internal/runtime/protocol -run TestTheCommittedSchemaMatchesThisBuild
	cd $(VSCODE_DIR) && $(NPM) run check:protocol

vscode-compatibility:
	$(GO) test ./internal/compatibility
	cd $(VSCODE_DIR) && $(NPM) run check:compatibility

vscode-check: vscode-install vscode-protocol-check vscode-compatibility
	cd $(VSCODE_DIR) && $(NPM) run check

vscode-test: vscode-install
	cd $(VSCODE_DIR) && $(NPM) test

vscode-security: vscode-install
	cd $(VSCODE_DIR) && $(NPM) run test:security
	cd $(VSCODE_DIR) && node ./scripts/matrix/record.mjs \
		security static host n/a n/a security

vscode-performance: build vscode-install
	cd $(VSCODE_DIR) && \
		CODEHELPER_VSCODE_BINARY='$(CURDIR)/$(BINARY)' \
		$(NPM) run test:performance && \
		CODEHELPER_VSCODE_BINARY='$(CURDIR)/$(BINARY)' \
		CODEHELPER_VSCODE_FIXTURE='$(CURDIR)/testdata/providers/openai' \
		$(NPM) run test:runtime-performance
	cd $(VSCODE_DIR) && node ./scripts/matrix/record.mjs \
		performance static host n/a n/a projector runtime-ready

# This is a real Go binary/stdio lifecycle gate without Electron. Extension
# Host integration remains a separate release gate.
vscode-runtime-integration: build vscode-install
	cd $(VSCODE_DIR) && \
		CODEHELPER_VSCODE_BINARY='$(CURDIR)/$(BINARY)' \
		CODEHELPER_VSCODE_FIXTURE='$(CURDIR)/testdata/providers/tools' \
		CODEHELPER_VSCODE_CONTEXT_FIXTURE='$(CURDIR)/testdata/providers/editor-context' \
		$(NPM) test

vscode-build: vscode-install
	cd $(VSCODE_DIR) && $(NPM) run build

# Downloads the pinned VS Code 1.96.4 Electron host on first use. Kept out of
# verify so ordinary repository checks never acquire a GUI runtime implicitly.
vscode-integration: build vscode-install
	cd $(VSCODE_DIR) && \
		CODEHELPER_VSCODE_BINARY='$(CURDIR)/$(BINARY)' \
		CODEHELPER_VSCODE_SELECTION_FIXTURE='$(CURDIR)/testdata/providers/selection-commands' \
		$(NPM) run test:electron

# Runs the x64 VS Code and Runtime under Rosetta on an Apple Silicon release
# host. The pinned x64 Electron host downloads on first use.
vscode-rosetta-integration: vscode-install
	@test "$$(uname -s)" = Darwin && test "$$(uname -m)" = arm64 || \
		{ printf '%s\n' 'Rosetta integration requires Apple Silicon macOS'; exit 1; }
	@tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath \
		-ldflags '$(LDFLAGS)' -o "$$tmp/codehelper" ./cmd/codehelper; \
	cd $(VSCODE_DIR) && \
		CODEHELPER_VSCODE_BINARY="$$tmp/codehelper" \
		CODEHELPER_VSCODE_SELECTION_FIXTURE='$(CURDIR)/testdata/providers/selection-commands' \
		CODEHELPER_VSCODE_TEST_PLATFORM=darwin \
		CODEHELPER_EXPECTED_HOST_ARCH=x64 \
		CODEHELPER_MATRIX_TARGET=darwin-x64 \
		CODEHELPER_ELECTRON_SCENARIOS=native,multi \
		CODEHELPER_VSCODE_DISABLE_GPU=1 \
		$(NPM) run test:electron

vscode-multiroot-integration: build vscode-install
	cd $(VSCODE_DIR) && \
		CODEHELPER_VSCODE_BINARY='$(CURDIR)/$(BINARY)' \
		CODEHELPER_VSCODE_SELECTION_FIXTURE='$(CURDIR)/testdata/providers/selection-commands' \
		CODEHELPER_ELECTRON_SCENARIOS=multi \
		$(NPM) run test:electron

vscode-update-integration: vscode-install
	cd $(VSCODE_DIR) && $(NPM) run test:update
	cd $(VSCODE_DIR) && node ./scripts/matrix/record.mjs \
		update-integration static host n/a managed \
		signature redirect truncation rollback revocation concurrency

vscode-package: vscode-install
	cd $(VSCODE_DIR) && $(NPM) run package:vsix

vscode-release-dry-run: vscode-install
	cd $(VSCODE_DIR) && $(NPM) run release:vscode:dry-run

vscode-distribution: vscode-release-dry-run
	cd $(VSCODE_DIR) && node ./scripts/matrix/record.mjs \
		distribution static multi-target n/a bundled \
		universal target-vsix sbom provenance checksums install handshake

vscode-local-setup: vscode-distribution
	./scripts/setup-vscode-local.sh --skip-build

deepseek-init:
	./scripts/deepseek-local.sh init

deepseek-tui:
	./scripts/deepseek-local.sh tui

deepseek-vscode:
	./scripts/deepseek-local.sh vscode

vscode-matrix-report:
	cd $(VSCODE_DIR) && $(NPM) run matrix:report

vscode-rc:
	$(MAKE) vscode-check
	$(MAKE) vscode-runtime-integration
	$(MAKE) vscode-security
	$(MAKE) vscode-performance
	$(MAKE) vscode-integration
	$(MAKE) vscode-rosetta-integration
	$(MAKE) vscode-update-integration
	$(MAKE) vscode-distribution
	$(MAKE) vscode-matrix-report
	cd $(VSCODE_DIR) && $(NPM) run release:vscode:rc

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
	$(GO) test -count=1 \
		-run 'TestBinaryInterop(ReplayPagesMatchLiveStream|RestartLoadsSessionAndReplays)' \
		./internal/host/runtimeapi/acp

# upgrade-baseline writes the versioned coding baseline. The report preserves
# failed task evidence and exits non-zero unless every declared task passes.
upgrade-baseline:
	$(GO) run ./scripts/upgradebaseline \
		--output '$(UPGRADE_BASELINE_REPORT)'

# catalog-bench tracks the M4 dynamic tool catalog's time, allocation, and
# prompt-size baseline at 100/500/1000 tools.
catalog-bench:
	$(GO) test -run '^$$' \
		-bench 'BenchmarkTool(Catalog|RegistryStartup)Scale' \
		-benchtime=10x -benchmem ./internal/runtime/agent/promptcontext

package: build
	VERSION='$(VERSION)' RELEASE_STAGE='$(RELEASE_STAGE)' ./scripts/package-release.sh

clean:
	rm -rf bin dist .tmp

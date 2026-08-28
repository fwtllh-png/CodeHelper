package wire

import (
	"log/slog"
	"os"
	"sync"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/platform/workspacequery"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type providerBundle struct {
	fixture           *fixture.Server
	providerID        string
	modelID           string
	modelCapabilities protocol.ModelCapabilities
	providerCatalog   protocol.ProviderCatalog
	modelCatalog      protocol.ModelCatalog
}

type platformBundle struct {
	processes       *process.SessionManager
	workspaceQuery  *workspacequery.Service
	repositoryIndex *repoindex.Index
	sandbox         sandbox.Backend
}

type extensionBundle struct {
	extensions *extensionSession
	mcpPool    *mcpruntime.Pool
	mcpPrewarm *MCPPrewarm
	memory     *memory.Store
}

type orchestrationBundle struct {
	inputHost        *interacttool.Host
	applyPlan        func(interacttool.Plan) error
	children         *childRuntime
	childTools       *childToolsets
	chatWorkspaces   *chatWorkspaces
	subagents        *subagent.AgentControl
	turnCoordinators *durableCoordinatorRuntime
}

type persistenceBundle struct {
	content           *contentstore.Memory
	jobLogs           *joblog.Store
	ephemeralState    *sqlitestate.Store
	ephemeralStateDir string
	journal           *workspacejournal.Manager
	journalRecovery   workspacejournal.Recovery
}

type securityBundle struct {
	security           *policy.Runtime
	Constitution       constitution.Status
	constitutionPrompt string
}

type observabilityBundle struct {
	metrics     *telemetry.Metrics
	metricsPath string
	logger      *slog.Logger
	logFile     *os.File
	traces      traceSession
}

type runtimeBundle struct {
	Runtime *app.Runtime
	threads *app.ThreadManager
}

type resourceBundle struct {
	resources *ResourceStack
	closeOnce sync.Once
	closeErr  error
}

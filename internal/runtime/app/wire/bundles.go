package wire

import (
	"log/slog"
	"os"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/platform/workspacequery"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
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
	plugins      []*pluginruntime.Loaded
	extensions   *extensionSession
	dynamicTools *dynamictool.Manager
	mcpPool      *mcpruntime.Pool
	mcpPrewarm   *MCPPrewarm
	memory       *memory.Store
	hooks        *hooks.Manager
}

type orchestrationBundle struct {
	automations      *automation.Repository
	tasks            *taskstate.Repository
	inputHost        *interacttool.Host
	applyPlan        func(interacttool.Plan) error
	rlmStore         *rlm.Store
	children         *childRuntime
	childTools       *childToolsets
	chatWorkspaces   *chatWorkspaces
	subagents        *subagent.AgentControl
	scheduler        *worker.Scheduler
	turnCoordinators *durableCoordinatorRuntime
}

type persistenceBundle struct {
	content           *contentstore.Memory
	jobLogs           *joblog.Store
	ephemeralTasks    *sqlitestate.Store
	ephemeralTasksDir string
	journal           *workspacejournal.Manager
	journalRecovery   workspacejournal.Recovery
}

type securityBundle struct {
	security           *policy.Runtime
	Constitution       constitution.Status
	constitutionPrompt string
}

type observabilityBundle struct {
	metrics       *telemetry.Metrics
	metricsPath   string
	logger        *slog.Logger
	logFile       *os.File
	observability observationSession
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

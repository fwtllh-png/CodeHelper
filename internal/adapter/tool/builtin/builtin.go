package builtin

import (
	"errors"

	language "github.com/fwtllh-png/CodeHelper/internal/adapter/lsp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	completiontool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/completion"
	contenttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/content"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	gittool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/git"
	githubtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/github"
	handletool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	lsptool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/lsp"
	qualitytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/quality"
	searchtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/search"
	shelltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/shell"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func NewWithSandboxBackend(root string, backend sandbox.Backend) (*tool.Registry, error) {
	registry, _, err := NewWithDependencies(
		root, backend, contentstore.NewMemory(contentstore.Options{}), process.NewSessionManager(0),
	)
	return registry, err
}

func NewWithDependencies(
	root string,
	backend sandbox.Backend,
	store contentstore.Store,
	manager *process.SessionManager,
	webOpts ...webtool.Options,
) (*tool.Registry, *handletool.Store, error) {
	return NewWithIndex(root, backend, store, manager, nil, webOpts...)
}

// NewWithIndex is NewWithDependencies with a repository index for the symbol
// tools. A nil index registers them as unavailable rather than hiding them, so
// the model learns why symbol lookup is not an option in this session.
func NewWithIndex(
	root string,
	backend sandbox.Backend,
	store contentstore.Store,
	manager *process.SessionManager,
	index *repoindex.Index,
	webOpts ...webtool.Options,
) (*tool.Registry, *handletool.Store, error) {
	if backend == nil {
		return nil, nil, errors.New("builtin tools require an injected sandbox backend")
	}
	if store == nil {
		store = contentstore.NewMemory(contentstore.Options{})
	}
	if manager == nil {
		return nil, nil, errors.New("builtin tools require an injected process manager")
	}
	var err error
	backend, err = sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		return nil, nil, err
	}
	registry := tool.NewRegistry(nil, tool.NewResultStoreWithStore(32<<10, store))
	registry.SetSandboxBackend(backend)
	handles := handletool.NewStore()
	if err := handletool.Register(registry, handles); err != nil {
		return nil, nil, err
	}
	if err := completiontool.Register(registry); err != nil {
		return nil, nil, err
	}
	files, err := filetool.NewWithBackend(root, backend)
	if err != nil {
		return nil, nil, err
	}
	if err := files.Register(registry); err != nil {
		return nil, nil, err
	}
	options := webtool.OptionsFromEnv()
	if len(webOpts) > 0 {
		merged := webOpts[0]
		if merged.SearchBackend == "" {
			merged.SearchBackend = options.SearchBackend
		}
		if merged.SearchURL == "" {
			merged.SearchURL = options.SearchURL
		}
		if merged.PrimaryURL == "" {
			merged.PrimaryURL = options.PrimaryURL
		}
		if merged.FallbackURL == "" {
			merged.FallbackURL = options.FallbackURL
		}
		if merged.TavilyURL == "" {
			merged.TavilyURL = options.TavilyURL
		}
		if merged.TavilyAPIKey == "" {
			merged.TavilyAPIKey = options.TavilyAPIKey
		}
		if merged.SearXNGURL == "" {
			merged.SearXNGURL = options.SearXNGURL
		}
		if merged.BochaURL == "" {
			merged.BochaURL = options.BochaURL
		}
		if merged.BochaAPIKey == "" {
			merged.BochaAPIKey = options.BochaAPIKey
		}
		if merged.Browser == nil {
			merged.Browser = options.Browser
		}
		if merged.HTTP == nil {
			merged.HTTP = options.HTTP
		}
		options = merged
	}
	if err := webtool.RegisterWithOptions(registry, options); err != nil {
		return nil, nil, err
	}
	if err := searchtool.RegisterWithProviders(
		registry, root, backend, index,
		language.Checker{Root: root, Sandbox: backend},
	); err != nil {
		return nil, nil, err
	}
	for _, register := range []func(*tool.Registry, string, sandbox.Backend) error{
		gittool.RegisterWithBackend,
		lsptool.RegisterWithBackend,
		contenttool.RegisterWithBackend,
	} {
		if err := register(registry, root, backend); err != nil {
			return nil, nil, err
		}
	}
	if err := shelltool.RegisterWithManagerAndBackend(
		registry, root, manager, backend,
	); err != nil {
		return nil, nil, err
	}
	if err := qualitytool.RegisterWithBackend(registry, root, backend); err != nil {
		return nil, nil, err
	}
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: root, Backend: backend,
	}); err != nil {
		return nil, nil, err
	}
	return registry, handles, nil
}

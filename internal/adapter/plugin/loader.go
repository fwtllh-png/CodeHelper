package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Loader struct {
	workspace *sandbox.Workspace
	backend   sandbox.Backend
	directory string
}

type Loaded struct {
	name       string
	version    string
	publisher  string
	trust      string
	executable string
	arguments  []string
	directory  string
	backend    sandbox.Backend
	inventory  CapabilityInventory
	cleanup    string
	authority  *authority
	onClose    func()
	mu         sync.Mutex
}

func NewLoader(workspaceRoot string, backend sandbox.Backend) (*Loader, error) {
	if backend == nil {
		return nil, errors.New("plugin loader requires an injected sandbox backend")
	}
	backend, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: workspaceRoot})
	if err != nil {
		return nil, err
	}
	workspace, err := sandbox.NewWorkspace(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return &Loader{
		workspace: workspace, backend: backend, directory: workspace.Root(),
	}, nil
}

func LoadReceipt(path string) (Receipt, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Receipt{}, err
	}
	parent, err := safeDirectory(filepath.Dir(absolute), false)
	if err != nil {
		return Receipt{}, fmt.Errorf("validate plugin receipt directory: %w", err)
	}
	directory, err := sandbox.NewWorkspace(parent)
	if err != nil {
		return Receipt{}, err
	}
	file, err := directory.OpenFile(filepath.Base(absolute))
	if err != nil {
		return Receipt{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return Receipt{}, err
	}
	if len(data) > 64<<10 {
		return Receipt{}, errors.New("plugin receipt exceeds size limit")
	}
	var receipt Receipt
	if err := decodeStrict(data, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode plugin receipt: %w", err)
	}
	if err := validateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (l *Loader) Load(bundleName string, receipt Receipt) (*Loaded, error) {
	if l == nil || l.workspace == nil {
		return nil, errors.New("plugin loader workspace is required")
	}
	bundleRoot, err := l.workspace.Resolve(bundleName, sandbox.MustExist)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin bundle: %w", err)
	}
	bundle, err := sandbox.NewWorkspace(bundleRoot)
	if err != nil {
		return nil, fmt.Errorf("open plugin bundle: %w", err)
	}
	manifest, err := ReadManifest(bundleRoot)
	if err != nil {
		return nil, err
	}
	if err := Verify(bundleRoot, manifest.Capabilities, manifest.Generation, receipt); err != nil {
		return nil, fmt.Errorf("verify plugin trust: %w", err)
	}
	executableFile, err := bundle.OpenFile(manifest.Executable)
	if err != nil {
		return nil, fmt.Errorf("open plugin executable: %w", err)
	}
	info, statErr := executableFile.Stat()
	if statErr != nil {
		executableFile.Close()
		return nil, statErr
	}
	if info.Mode().Perm()&0o111 == 0 {
		executableFile.Close()
		return nil, errors.New("plugin executable is not executable")
	}
	snapshot, err := os.CreateTemp("", "codehelper-plugin-exec-*")
	if err != nil {
		executableFile.Close()
		return nil, err
	}
	snapshotPath := snapshot.Name()
	cleanup := func() { _ = os.Remove(snapshotPath) }
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(snapshot, hash), io.LimitReader(executableFile, maxBundleBytes+1))
	closeSourceErr := executableFile.Close()
	if copyErr == nil {
		copyErr = snapshot.Chmod(0o700)
	}
	if copyErr == nil {
		copyErr = snapshot.Sync()
	}
	closeSnapshotErr := snapshot.Close()
	if copyErr != nil || closeSourceErr != nil || closeSnapshotErr != nil {
		cleanup()
		return nil, errors.Join(copyErr, closeSourceErr, closeSnapshotErr)
	}
	if written > maxBundleBytes ||
		!equalHash(hex.EncodeToString(hash.Sum(nil)), manifest.ExecutableSHA256) {
		cleanup()
		return nil, errors.New("plugin executable hash does not match trusted manifest")
	}
	return &Loaded{
		name: manifest.Name, version: manifest.Version,
		publisher: manifest.Publisher, trust: receipt.Trust,
		executable: snapshotPath,
		arguments:  append([]string(nil), manifest.Arguments...),
		directory:  l.directory, backend: l.backend,
		inventory: manifest.Capabilities,
		cleanup:   snapshotPath,
	}, nil
}

func (p *Loaded) Version() string {
	if p == nil {
		return ""
	}
	if p.version == "" {
		return "local"
	}
	return p.version
}

func (p *Loaded) Publisher() string {
	if p == nil {
		return ""
	}
	return p.publisher
}

func (p *Loaded) Trust() string {
	if p == nil {
		return ""
	}
	if p.trust == "" {
		return TrustUnsignedLocal
	}
	return p.trust
}

func (p *Loaded) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

func (p *Loaded) Inventory() CapabilityInventory {
	if p == nil {
		return CapabilityInventory{}
	}
	value, _ := normalizeCapabilities(p.inventory)
	return value
}

func (p *Loaded) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.cleanup == "" {
		p.mu.Unlock()
		return nil
	}
	path := p.cleanup
	p.cleanup = ""
	onClose := p.onClose
	p.onClose = nil
	p.mu.Unlock()
	removeErr := os.Remove(path)
	if onClose != nil {
		onClose()
	}
	return removeErr
}

func (p *Loaded) Run(ctx context.Context, arguments json.RawMessage) (process.Result, error) {
	if p == nil {
		return process.Result{}, errors.New("loaded plugin is required")
	}
	p.mu.Lock()
	if p.cleanup == "" {
		p.mu.Unlock()
		return process.Result{}, errors.New("loaded plugin is closed")
	}
	executable := p.executable
	argumentsPrefix := append([]string(nil), p.arguments...)
	directoryPath := p.directory
	backend := p.backend
	auth := p.authority
	p.mu.Unlock()
	if auth != nil {
		var cancel context.CancelFunc
		ctx, cancel = auth.bind(ctx)
		defer cancel()
		if err := auth.check(); err != nil {
			return process.Result{}, err
		}
	}
	canonical, err := canonicalObject(arguments)
	if err != nil {
		return process.Result{}, err
	}
	commandArguments := argumentsPrefix
	commandArguments = append(commandArguments, string(canonical))
	directory, err := process.OpenPinnedDirectory(backend, directoryPath)
	if err != nil {
		return process.Result{}, err
	}
	defer directory.Close()
	return process.Run(ctx, process.Options{
		Path: executable, Args: commandArguments, Dir: directoryPath,
		DirFile: directory, Sandbox: backend, RequireStrongSandbox: true,
		WorkspaceReadOnly: true,
	})
}

func canonicalObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value map[string]any
	if err := decodeStrict(raw, &value); err != nil {
		return nil, errors.New("plugin arguments must be one JSON object")
	}
	return json.Marshal(value)
}

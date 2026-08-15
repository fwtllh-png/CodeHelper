package process

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Options struct {
	Command              string
	Path                 string
	Args                 []string
	Dir                  string
	DirFile              *os.File
	PTY                  bool
	Env                  []string
	Sandbox              sandbox.Backend
	RequireStrongSandbox bool
	WorkspaceReadOnly    bool
	WorkspaceWritePaths  []string
	DenyNetwork          bool
	// OnOutput, when set, is called with each chunk as the process produces it,
	// before the command finishes. A caller that only wants the final Result can
	// leave it nil; a caller that has to show progress on a command that runs for
	// a minute cannot wait for Result to exist.
	//
	// It is called from the reader goroutines, so it must be cheap and must not
	// block: a slow observer holds up the pipe and thereby the process itself.
	OnOutput func(Chunk)
	// OutputLimitBytes bounds retained bytes independently for stdout and
	// stderr. Zero selects DefaultOutputLimitBytes.
	OutputLimitBytes int
	// ArchiveOutput receives every complete chunk even when the returned
	// head/tail result is truncated.
	ArchiveOutput OutputArchive
}

// Stream names which of a process's two output streams a chunk came from. PTY
// execution merges them, and reports everything as StreamStdout, because that is
// what a terminal does.
type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

// Chunk is one piece of output as it was read, with the number of bytes of that
// stream delivered through the end of this chunk. The cursor lets an observer
// notice it missed something rather than silently rendering a gap.
type Chunk struct {
	Stream Stream
	Data   []byte
	Cursor uint64
}

type Result struct {
	Stdout        string
	Stderr        string
	ExitCode      int
	OutputReceipt OutputReceipt
}

func OpenPinnedDirectory(backend sandbox.Backend, directory string) (*os.File, error) {
	policy, ok := sandbox.BackendPolicy(backend)
	if !ok || policy.WorkspaceRoot == "" {
		return nil, errors.New("sandbox backend has no workspace policy")
	}
	workspace, err := sandbox.NewWorkspace(policy.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(policy.WorkspaceRoot, directory)
	if err != nil {
		return nil, err
	}
	return workspace.OpenDirectory(relative)
}

func Run(ctx context.Context, options Options) (Result, error) {
	if options.OutputLimitBytes < 0 {
		return Result{}, errors.New("process output limit must not be negative")
	}
	command, err := NewCommand(ctx, options)
	if err != nil {
		return Result{}, err
	}
	limit := options.OutputLimitBytes
	if limit == 0 {
		limit = DefaultOutputLimitBytes
	}
	var archive *archiveState
	if options.ArchiveOutput != nil {
		archive = &archiveState{append: options.ArchiveOutput}
	}
	if options.PTY {
		return runPTY(ctx, command, options.OnOutput, archive, limit)
	}
	stdout := newObservedBuffer(StreamStdout, limit, options.OnOutput, archive)
	stderr := newObservedBuffer(StreamStderr, limit, options.OnOutput, archive)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return Result{}, err
	}
	err = waitAndCancel(ctx, command)
	result := Result{
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: ExitCode(err),
		OutputReceipt: OutputReceipt{
			Stdout: stdout.Receipt(), Stderr: stderr.Receipt(),
			ArchiveError: archive.errorString(),
		},
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}

func NewCommand(ctx context.Context, options Options) (*exec.Cmd, error) {
	environment, err := SanitizedEnvironment(options.Env)
	if err != nil {
		return nil, err
	}
	environment = ensureGoToolchain(environment)
	environment = ensureGitToolchain(environment)
	if options.WorkspaceReadOnly {
		environment = setEnvironmentValue(environment, "GIT_OPTIONAL_LOCKS", "0")
		environment = setEnvironmentValue(environment, "PYTHONDONTWRITEBYTECODE", "1")
	}
	if strings.TrimSpace(options.Dir) == "" {
		return nil, errors.New("child process directory is required")
	}
	if (options.Command == "") == (options.Path == "") {
		return nil, errors.New("exactly one of shell command or executable path is required")
	}
	commandSpec := sandbox.Command{
		Dir: options.Dir, Env: environment,
		WorkspaceReadOnly:   options.WorkspaceReadOnly,
		WorkspaceWritePaths: append([]string(nil), options.WorkspaceWritePaths...),
		DenyNetwork:         options.DenyNetwork,
	}
	if options.DirFile != nil {
		commandSpec.DirectoryFD = 3
	}
	if options.Path != "" {
		if strings.IndexByte(options.Path, 0) >= 0 {
			return nil, errors.New("executable path contains NUL")
		}
		commandSpec.Path = options.Path
		commandSpec.Args = append([]string{options.Path}, options.Args...)
	} else {
		commandSpec.Path = "sh"
		// A macOS login shell runs path_helper and reorders PATH, which can put
		// Apple's /usr/bin tool shims ahead of the concrete toolchains selected
		// above. Restore the sanitized PATH after profile loading. Passing both
		// values positionally keeps the caller's command out of this wrapper.
		commandSpec.Args = []string{
			"sh", "-lc",
			`codehelper_path=$1; codehelper_command=$2; shift 2; ` +
				`PATH=$codehelper_path; export PATH; eval "$codehelper_command"`,
			"sh", environmentValue(environment, "PATH"), options.Command,
		}
	}
	if options.RequireStrongSandbox {
		if err := sandbox.RequireStrong(options.Sandbox); err != nil {
			return nil, err
		}
		if options.DirFile == nil {
			return nil, errors.New("strong sandbox execution requires a pinned workspace cwd descriptor")
		}
	}
	if len(options.WorkspaceWritePaths) != 0 && !options.WorkspaceReadOnly {
		return nil, errors.New("workspace write paths require a read-only workspace base")
	}
	if (options.WorkspaceReadOnly || options.DenyNetwork ||
		len(options.WorkspaceWritePaths) != 0) && options.Sandbox == nil {
		return nil, errors.New("process restrictions require a sandbox backend")
	}
	if options.Sandbox != nil {
		resolved, resolveErr := sandbox.ResolveExecutable(commandSpec.Path, environment)
		if resolveErr != nil {
			return nil, resolveErr
		}
		commandSpec.Path = resolved
		commandSpec.Args[0] = resolved
		policy, ok := sandbox.BackendPolicy(options.Sandbox)
		if options.RequireStrongSandbox && !ok {
			return nil, errors.New("strong sandbox backend has no prepared policy identity")
		}
		environment = sandboxEnvironment(environment, policy)
		commandSpec.Env = environment
		commandSpec, err = options.Sandbox.Prepare(ctx, commandSpec)
		if err != nil {
			return nil, err
		}
		if options.RequireStrongSandbox &&
			(commandSpec.PreparedPolicyID == "" ||
				commandSpec.PreparedPolicyID != policy.ID ||
				commandSpec.PreparedStrength != sandbox.StrengthStrong) {
			return nil, errors.New("strong sandbox backend returned an unverified prepared policy")
		}
		if options.WorkspaceReadOnly && !commandSpec.PreparedReadOnly {
			return nil, errors.New("sandbox backend did not enforce a read-only workspace")
		}
		if !slices.Equal(options.WorkspaceWritePaths, commandSpec.PreparedWritePaths) {
			return nil, errors.New("sandbox backend did not enforce exact workspace write paths")
		}
		if options.DenyNetwork && !commandSpec.PreparedNetworkDenied {
			return nil, errors.New("sandbox backend did not enforce network isolation")
		}
	}
	if commandSpec.Path == "" || len(commandSpec.Args) == 0 {
		return nil, errors.New("sandbox returned an invalid command")
	}
	command, err := commandForSpec(ctx, commandSpec, options.DirFile)
	if err != nil {
		return nil, err
	}
	configureProcessGroup(command, options.PTY)
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return terminateProcessGroup(command.Process)
	}
	command.WaitDelay = 2 * time.Second
	return command, nil
}

func sandboxEnvironment(environment []string, policy sandbox.Policy) []string {
	if policy.PrivateTemp == "" {
		return environment
	}
	result := make([]string, 0, len(environment)+5)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "HOME=") || strings.HasPrefix(entry, "TMPDIR=") ||
			strings.HasPrefix(entry, "TMP=") || strings.HasPrefix(entry, "TEMP=") ||
			strings.HasPrefix(entry, "GOTMPDIR=") ||
			strings.HasPrefix(entry, "GOCACHE=") || strings.HasPrefix(entry, "GOMODCACHE=") {
			continue
		}
		result = append(result, entry)
	}
	result = append(result, "HOME="+policy.PrivateTemp, "TMPDIR="+policy.PrivateTemp)
	// Keep Go toolchain caches inside the writable private temp. GOTMPDIR covers
	// compile work dirs separately from GOCACHE.
	result = append(result,
		"GOTMPDIR="+policy.PrivateTemp,
		"GOCACHE="+filepath.Join(policy.PrivateTemp, "go-build"),
		"GOMODCACHE="+filepath.Join(policy.PrivateTemp, "pkg", "mod"),
	)
	return result
}

func ensureGoToolchain(environment []string) []string {
	root := environmentValue(environment, "GOROOT")
	if root == "" {
		root = strings.TrimSpace(runtime.GOROOT())
		if root != "" {
			environment = append(append([]string(nil), environment...), "GOROOT="+root)
		}
	}
	var prepend []string
	if root != "" {
		bin := filepath.Join(root, "bin")
		if info, err := os.Stat(bin); err == nil && info.IsDir() {
			prepend = append(prepend, bin)
		}
	}
	if goBin, err := exec.LookPath("go"); err == nil {
		prepend = append(prepend, filepath.Dir(goBin))
		if resolved, err := filepath.EvalSymlinks(goBin); err == nil {
			prepend = append(prepend, filepath.Dir(resolved))
		}
	}
	return prependPATH(environment, prepend...)
}

func ensureGitToolchain(environment []string) []string {
	if runtime.GOOS != "darwin" {
		return environment
	}
	for _, candidate := range []string{
		"/Library/Developer/CommandLineTools/usr/bin/git",
		"/Applications/Xcode.app/Contents/Developer/usr/bin/git",
	} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return prependPATH(environment, filepath.Dir(candidate))
		}
	}
	return environment
}

func prependPATH(environment []string, directories ...string) []string {
	if len(directories) == 0 {
		return environment
	}
	pathValue := environmentValue(environment, "PATH")
	parts := filepath.SplitList(pathValue)
	seen := make(map[string]bool, len(parts)+len(directories))
	for _, part := range parts {
		seen[filepath.Clean(part)] = true
	}
	var prefix []string
	for _, directory := range directories {
		if directory == "" || !filepath.IsAbs(directory) {
			continue
		}
		clean := filepath.Clean(directory)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		prefix = append(prefix, clean)
	}
	if len(prefix) == 0 {
		return environment
	}
	newPath := strings.Join(append(prefix, parts...), string(os.PathListSeparator))
	result := make([]string, 0, len(environment)+1)
	replaced := false
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			result = append(result, "PATH="+newPath)
			replaced = true
			continue
		}
		result = append(result, entry)
	}
	if !replaced {
		result = append(result, "PATH="+newPath)
	}
	return result
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return entry[len(prefix):]
		}
	}
	return ""
}

func setEnvironmentValue(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	replaced := false
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				result = append(result, prefix+value)
				replaced = true
			}
			continue
		}
		result = append(result, entry)
	}
	if !replaced {
		result = append(result, prefix+value)
	}
	return result
}

// observedBuffer retains bounded head/tail output while forwarding complete
// chunks to live observers and an optional durable archive.
type observedBuffer struct {
	buffer  *headTailBuffer
	stream  Stream
	observe func(Chunk)
	archive *archiveState
	cursor  uint64
}

func newObservedBuffer(
	stream Stream,
	limit int,
	observe func(Chunk),
	archive *archiveState,
) *observedBuffer {
	return &observedBuffer{
		buffer: newHeadTailBuffer(limit), stream: stream, observe: observe, archive: archive,
	}
}

func (o *observedBuffer) Write(data []byte) (int, error) {
	count, err := o.buffer.Write(data)
	if count > 0 {
		o.cursor += uint64(count)
		if o.observe != nil || o.archive != nil {
			// Readers reuse their slice. Both consumers receive a stable copy for
			// the duration of their synchronous callback.
			chunkData := append([]byte(nil), data[:count]...)
			chunk := Chunk{Stream: o.stream, Data: chunkData, Cursor: o.cursor}
			o.archive.write(chunk)
			if o.observe != nil {
				o.observe(chunk)
			}
		}
	}
	return count, err
}

func (o *observedBuffer) String() string { return o.buffer.String() }

func (o *observedBuffer) Receipt() StreamReceipt { return o.buffer.Receipt() }

func runPTY(
	ctx context.Context,
	command *exec.Cmd,
	observe func(Chunk),
	archive *archiveState,
	limit int,
) (Result, error) {
	terminal, err := pty.Start(command)
	if err != nil {
		return Result{}, err
	}
	// A pty merges the two streams, so everything it produces is reported as
	// stdout rather than guessed at.
	output := newObservedBuffer(StreamStdout, limit, observe, archive)
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(output, terminal)
		copyDone <- copyErr
	}()
	waitErr := waitAndCancel(ctx, command)
	_ = terminal.Close()
	copyErr := <-copyDone
	if copyErr != nil &&
		!errors.Is(copyErr, syscall.EIO) &&
		!errors.Is(copyErr, os.ErrClosed) &&
		ctx.Err() == nil {
		return Result{}, copyErr
	}
	result := Result{
		Stdout: output.String(), ExitCode: ExitCode(waitErr),
		OutputReceipt: OutputReceipt{
			Stdout: output.Receipt(), ArchiveError: archive.errorString(),
		},
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}

func waitAndCancel(ctx context.Context, command *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = terminateProcessGroup(command.Process)
		<-done
		return ctx.Err()
	}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

package artifactbroker

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fwtllh-png/QCode/internal/security/authority"
)

type Options struct {
	WorkspaceRoot       string
	SandboxHomeRoot     string
	StagingRoot         string
	WorkspaceID         string
	WorkspaceGeneration uint64
	Random              io.Reader
}

type PrepareRequest struct {
	SourcePath              string
	ProducerOperationDigest string
}

type Snapshot struct {
	Manifest       authority.ArtifactManifest
	Root           string
	ExecutablePath string
}

type Broker struct {
	workspaceRoot       string
	sandboxHomeRoot     string
	stagingRoot         string
	workspaceID         string
	workspaceGeneration uint64
	random              io.Reader
	mu                  sync.Mutex
	generation          uint64
}

func New(options Options) (*Broker, error) {
	if options.WorkspaceGeneration == 0 ||
		strings.TrimSpace(options.WorkspaceID) == "" {
		return nil, errors.New("artifact broker requires a Workspace identity")
	}
	workspace, err := canonicalRoot(options.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact Workspace root: %w", err)
	}
	home, err := canonicalRoot(options.SandboxHomeRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact Sandbox Home root: %w", err)
	}
	stage, err := canonicalRoot(options.StagingRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact staging root: %w", err)
	}
	if contains(workspace, stage) || contains(home, stage) ||
		contains(stage, workspace) || contains(stage, home) {
		return nil, errors.New("artifact staging must not overlap source roots")
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Broker{
		workspaceRoot: workspace, sandboxHomeRoot: home,
		stagingRoot: stage, workspaceID: options.WorkspaceID,
		workspaceGeneration: options.WorkspaceGeneration,
		random:              options.Random,
	}, nil
}

func (b *Broker) Prepare(request PrepareRequest) (_ Snapshot, resultErr error) {
	if b == nil {
		return Snapshot{}, errors.New("artifact broker is required")
	}
	if !validDigest(request.ProducerOperationDigest) {
		return Snapshot{}, errors.New("artifact producer operation digest is invalid")
	}
	source, err := filepath.Abs(request.SourcePath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve artifact source: %w", err)
	}
	source = filepath.Clean(source)
	inputInfo, err := os.Lstat(source)
	if err != nil {
		return Snapshot{}, err
	}
	if inputInfo.Mode()&os.ModeSymlink != 0 {
		return Snapshot{}, errors.New("artifact source path contains a symbolic link")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return Snapshot{}, err
	}
	root, relative, err := b.sourceRoot(source)
	if err != nil {
		return Snapshot{}, err
	}
	if err := rejectLinkedPath(root, relative); err != nil {
		return Snapshot{}, err
	}
	sourceRootInfo, err := os.Stat(root)
	if err != nil {
		return Snapshot{}, err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return Snapshot{}, err
	}
	defer sourceFile.Close()
	before, err := sourceFile.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateSourceFile(
		source, root, sourceFile, before, sourceRootInfo,
	); err != nil {
		return Snapshot{}, err
	}

	id, err := randomID(b.random)
	if err != nil {
		return Snapshot{}, err
	}
	b.mu.Lock()
	b.generation++
	generation := b.generation
	b.mu.Unlock()

	stageRoot, err := os.OpenRoot(b.stagingRoot)
	if err != nil {
		return Snapshot{}, err
	}
	defer stageRoot.Close()
	tempName := "." + id + ".tmp"
	finalName := id
	if err := stageRoot.Mkdir(tempName, 0o700); err != nil {
		return Snapshot{}, err
	}
	committed := false
	defer func() {
		if !committed {
			resultErr = errors.Join(resultErr, stageRoot.RemoveAll(tempName))
		}
	}()
	destination, err := stageRoot.OpenFile(
		filepath.Join(tempName, "payload"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o500,
	)
	if err != nil {
		return Snapshot{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(destination, hash), sourceFile)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		return Snapshot{}, errors.Join(copyErr, closeErr)
	}
	after, err := sourceFile.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	if !os.SameFile(before, after) ||
		before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) {
		return Snapshot{}, errors.New("artifact source changed while snapshotting")
	}
	if size != before.Size() {
		return Snapshot{}, errors.New("artifact snapshot size changed while copying")
	}
	entry := authority.ArtifactEntry{
		Path: "payload", Digest: hex.EncodeToString(hash.Sum(nil)),
		Mode: uint32(before.Mode().Perm()), Size: size,
		Executable: before.Mode().Perm()&0o111 != 0,
	}
	manifest, err := authority.NewArtifactManifest(authority.ArtifactManifest{
		ID: id, Generation: generation,
		SourceWorkspaceID:         b.workspaceID,
		SourceWorkspaceGeneration: b.workspaceGeneration,
		ProducerOperationDigest:   request.ProducerOperationDigest,
		Entries:                   []authority.ArtifactEntry{entry},
	})
	if err != nil {
		return Snapshot{}, err
	}
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		return Snapshot{}, err
	}
	if err := stageRoot.WriteFile(
		filepath.Join(tempName, "manifest.json"),
		encodedManifest,
		0o400,
	); err != nil {
		return Snapshot{}, err
	}
	if err := stageRoot.Rename(tempName, finalName); err != nil {
		return Snapshot{}, err
	}
	committed = true
	return Snapshot{
		Manifest:       manifest,
		Root:           filepath.Join(b.stagingRoot, finalName),
		ExecutablePath: filepath.Join(b.stagingRoot, finalName, "payload"),
	}, nil
}

func (b *Broker) Release(snapshot Snapshot) error {
	if b == nil {
		return errors.New("artifact broker is required")
	}
	if err := snapshot.Manifest.Validate(); err != nil {
		return err
	}
	expectedRoot := filepath.Join(b.stagingRoot, snapshot.Manifest.ID)
	if filepath.Clean(snapshot.Root) != expectedRoot ||
		filepath.Clean(snapshot.ExecutablePath) != filepath.Join(expectedRoot, "payload") {
		return errors.New("artifact snapshot does not belong to this broker")
	}
	root, err := os.OpenRoot(b.stagingRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(snapshot.Manifest.ID)
}

func (b *Broker) sourceRoot(source string) (string, string, error) {
	for _, root := range []string{b.workspaceRoot, b.sandboxHomeRoot} {
		relative, err := filepath.Rel(root, source)
		if err == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return root, relative, nil
		}
	}
	return "", "", errors.New("artifact source is outside Workspace and Sandbox Home")
}

func canonicalRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func rejectLinkedPath(root, relative string) error {
	current := root
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("artifact source path contains a symbolic link")
		}
	}
	return nil
}

func validateSourceFile(
	sourcePath, rootPath string,
	source *os.File,
	file, root os.FileInfo,
) error {
	if !file.Mode().IsRegular() {
		return errors.New("artifact source must be a regular file")
	}
	if file.Mode().Perm()&0o111 == 0 {
		return errors.New("artifact source must be executable")
	}
	if linkedFile(sourcePath, source, file) {
		return errors.New("artifact source must not be hard-linked")
	}
	if !sameDevice(sourcePath, rootPath, source, file, root) {
		return errors.New("artifact source crosses a device boundary")
	}
	return nil
}

func randomID(reader io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(reader, value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

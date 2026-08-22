package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

const WorkspaceIdentityVersion = 1

// WorkspaceIdentity binds a host-visible workspace URI to the filesystem root
// visible to the Runtime process.
type WorkspaceIdentity struct {
	Version     int    `json:"version"`
	RootID      string `json:"root_id"`
	EditorURI   string `json:"editor_uri"`
	RuntimePath string `json:"runtime_path"`
	RemoteName  string `json:"remote_name,omitempty"`
}

func NewWorkspaceIdentity(editorURI, runtimePath, remoteName string) (WorkspaceIdentity, error) {
	identity := WorkspaceIdentity{
		Version: WorkspaceIdentityVersion, EditorURI: editorURI,
		RuntimePath: runtimePath, RemoteName: remoteName,
	}
	sum := sha256.Sum256([]byte(editorURI))
	identity.RootID = hex.EncodeToString(sum[:])
	if err := identity.Validate(); err != nil {
		return WorkspaceIdentity{}, err
	}
	return identity, nil
}

func (i WorkspaceIdentity) Validate() error {
	if i.Version != WorkspaceIdentityVersion {
		return errors.New("workspace identity version is unsupported")
	}
	if len(i.EditorURI) == 0 || len(i.EditorURI) > 4096 ||
		len(i.RuntimePath) == 0 || len(i.RuntimePath) > 4096 ||
		len(i.RemoteName) > 128 || !filepath.IsAbs(i.RuntimePath) {
		return errors.New("workspace identity fields are invalid")
	}
	sum := sha256.Sum256([]byte(i.EditorURI))
	if i.RootID != hex.EncodeToString(sum[:]) {
		return errors.New("workspace identity root id does not match editor uri")
	}
	uri, err := parseCanonicalWorkspaceURI(i.EditorURI)
	if err != nil {
		return err
	}
	if uri.Scheme != "file" {
		return errors.New("workspace identity uri scheme is unsupported")
	}
	if uri.Host != "" || i.RemoteName != "" {
		return errors.New("local workspace identity cannot carry authority or remote name")
	}
	return nil
}

func parseCanonicalWorkspaceURI(raw string) (*url.URL, error) {
	uri, err := url.Parse(raw)
	if err != nil || uri.Scheme == "" || uri.User != nil ||
		uri.RawQuery != "" || uri.Fragment != "" ||
		uri.Path == "" || !strings.HasPrefix(uri.Path, "/") ||
		path.Clean(uri.Path) != uri.Path || !canonicalPercentEscapes(raw) ||
		uri.String() != raw {
		return nil, errors.New("workspace identity editor uri is not canonical")
	}
	return uri, nil
}

func canonicalPercentEscapes(value string) bool {
	const hexDigits = "0123456789ABCDEF"
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) ||
			!strings.ContainsRune(hexDigits, rune(value[index+1])) ||
			!strings.ContainsRune(hexDigits, rune(value[index+2])) {
			return false
		}
		index += 2
	}
	return true
}

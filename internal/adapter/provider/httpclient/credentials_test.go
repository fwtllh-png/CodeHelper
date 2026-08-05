package httpclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestCredentialsResolveEnvironmentFileAndInjectedKeyring(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "provider"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "provider", "default"), []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := Credentials{
		LookupEnv: func(name string) (string, bool) {
			return map[string]string{"API_KEY": "env-secret"}[name], name == "API_KEY"
		},
		SecretDir: root,
		Keyring: staticKeyring{
			values: map[string]string{"provider/default": "keyring-secret"},
		},
	}
	for _, test := range []struct {
		kind string
		name string
		want string
	}{
		{kind: "env", name: "API_KEY", want: "env-secret"},
		{kind: "file", name: "provider/default", want: "file-secret"},
		{kind: "keyring", name: "provider/default", want: "keyring-secret"},
	} {
		value, err := resolver.Resolve(t.Context(), model.CredentialRef{Kind: test.kind, Name: test.name})
		if err != nil {
			t.Fatalf("Resolve(%s): %v", test.kind, err)
		}
		if value != test.want {
			t.Fatalf("Resolve(%s) = %q, want %q", test.kind, value, test.want)
		}
	}
}

func TestFileCredentialsRejectMissingInsecureSymlinkAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "insecure"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	resolver := Credentials{SecretDir: root}
	for _, name := range []string{"missing", "insecure", "linked", "../outside"} {
		if value, err := resolver.Resolve(t.Context(), model.CredentialRef{Kind: "file", Name: name}); err == nil || value != "" {
			t.Fatalf("Resolve(file:%s) = %q, %v", name, value, err)
		}
	}
}

func TestKeyringCredentialErrorsDoNotLeakSecretValues(t *testing.T) {
	const secret = "keyring-error-secret-sentinel"
	resolver := Credentials{Keyring: staticKeyring{err: errors.New(secret)}}
	_, err := resolver.Resolve(t.Context(), model.CredentialRef{
		Kind: "keyring", Name: "provider/default",
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Resolve() error = %v", err)
	}
}

type staticKeyring struct {
	values map[string]string
	err    error
}

func (k staticKeyring) Lookup(_ context.Context, name string) (string, error) {
	if k.err != nil {
		return "", k.err
	}
	value, exists := k.values[name]
	if !exists {
		return "", errors.New("missing")
	}
	return value, nil
}

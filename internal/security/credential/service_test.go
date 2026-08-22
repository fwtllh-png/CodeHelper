package credential

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryStore struct {
	values    map[string]string
	deleteErr error
}

func (s *memoryStore) Lookup(_ context.Context, name string) (string, error) {
	value, ok := s.values[name]
	if !ok {
		return "", errors.New("missing")
	}
	return value, nil
}

func (s *memoryStore) Set(name, value string) error {
	s.values[name] = value
	return nil
}

func (s *memoryStore) Delete(name string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.values, name)
	return nil
}

func TestKeyringLifecycleNeverReturnsSecret(t *testing.T) {
	storage := &memoryStore{values: map[string]string{}}
	service := &Service{
		reference: Reference{Kind: "keyring", Name: "workspace/provider"},
		keyring:   storage,
		probe: func(context.Context, Reference) error {
			return nil
		},
	}
	status, err := service.SetKeyring(t.Context(), "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.Reference.Name != "workspace/provider" {
		t.Fatalf("status = %+v", status)
	}
	validated, err := service.Validate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if validated.Validation != "valid" || validated.ValidatedAt == nil {
		t.Fatalf("validated = %+v", validated)
	}
	if _, err := service.ClearKeyring(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := storage.values["workspace/provider"]; ok {
		t.Fatal("credential was not removed")
	}
}

func TestSetKeyringRejectsNonKeyringReference(t *testing.T) {
	service := &Service{
		reference: Reference{Kind: "env", Name: "API_KEY"},
		keyring:   &memoryStore{values: map[string]string{}},
	}
	if _, err := service.SetKeyring(t.Context(), "secret"); err == nil {
		t.Fatal("expected non-keyring reference rejection")
	}
}

func TestValidateRequiresProviderProbeAndSanitizesFailure(t *testing.T) {
	storage := &memoryStore{values: map[string]string{
		"workspace/provider": "secret-value",
	}}
	service := &Service{
		reference: Reference{Kind: "keyring", Name: "workspace/provider"},
		keyring:   storage,
	}
	status, err := service.Validate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Validation != "not_validated" || status.ValidatedAt == nil {
		t.Fatalf("status without probe = %+v", status)
	}

	service.probe = func(context.Context, Reference) error {
		return errors.New("upstream rejected secret-value")
	}
	status, err = service.Validate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Validation != "invalid" ||
		status.ValidationDetail != "provider probe failed" {
		t.Fatalf("failed probe status = %+v", status)
	}
	if status.ValidationDetail == "upstream rejected secret-value" {
		t.Fatal("provider error leaked through credential status")
	}
}

func TestCredentialControlRecoversRotationAndDeferredCleanup(t *testing.T) {
	registry := t.TempDir()
	root := filepath.Join(registry, "workspace")
	storage := &memoryStore{values: map[string]string{
		"provider/old": "old-secret",
	}}
	control := &Control{
		root: root, registry: registry, namespace: "workspace-provider",
		base:    Reference{Kind: "keyring", Name: "provider/old"},
		keyring: storage,
	}
	service := New(
		control.base,
		WithControl(control),
		WithProbe(func(context.Context, Reference) error { return nil }),
	)
	status, err := service.SetKeyring(t.Context(), "new-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || !status.RestartRequired ||
		status.Validation != "valid" ||
		status.Reference.Kind != "keyring" ||
		status.Reference.Name == "provider/old" {
		t.Fatalf("rotated status = %+v", status)
	}
	if storage.values["provider/old"] != "old-secret" {
		t.Fatal("active Runtime credential was deleted before restart")
	}
	if storage.values[status.Reference.Name] != "new-secret" {
		t.Fatal("rotated credential was not written")
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "new-secret") ||
			strings.Contains(string(data), "old-secret") {
			t.Fatalf("secret leaked into %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	restarted := &Control{
		root: root, registry: registry, namespace: "workspace-provider",
		base: control.base, keyring: storage,
	}
	if err := restarted.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if storage.values["provider/old"] != "old-secret" {
		t.Fatal("external credential reference was deleted without a complete reference scan")
	}
	if storage.values[status.Reference.Name] != "new-secret" {
		t.Fatal("committed credential was removed during recovery")
	}

	config, err := restarted.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	restartedService := New(config.Reference, WithControl(restarted))
	second, err := restartedService.SetKeyring(t.Context(), "newer-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, exists := storage.values[status.Reference.Name]; exists {
		t.Fatal("unreferenced managed credential was not removed after restart")
	}
	if storage.values[second.Reference.Name] != "newer-secret" {
		t.Fatal("latest managed credential was removed during recovery")
	}
	cleared, err := restartedService.ClearKeyring(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Configured || !cleared.RestartRequired {
		t.Fatalf("cleared status = %+v", cleared)
	}
	if storage.values[second.Reference.Name] != "newer-secret" {
		t.Fatal("active credential was deleted before clear restart")
	}
	if err := restarted.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, exists := storage.values[second.Reference.Name]; exists {
		t.Fatal("cleared credential was not removed after restart")
	}
}

func TestCredentialControlRemovesPreparedOrphan(t *testing.T) {
	registry := t.TempDir()
	storage := &memoryStore{values: map[string]string{
		"web/workspace-provider/00000000000000000000000000000000": "orphan-secret",
	}}
	control := &Control{
		root:     filepath.Join(registry, "workspace"),
		registry: registry, namespace: "workspace-provider",
		base: Reference{Kind: "env", Name: "API_KEY"}, keyring: storage,
	}
	if err := control.writeIntent(recoveryIntent{
		Version: 1, OperationID: "operation",
		OldReference: control.base,
		NewReference: Reference{
			Kind: "keyring",
			Name: "web/workspace-provider/00000000000000000000000000000000",
		},
		Phase: "prepared",
	}); err != nil {
		t.Fatal(err)
	}
	if err := control.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, exists := storage.values["web/workspace-provider/00000000000000000000000000000000"]; exists {
		t.Fatal("prepared orphan credential was not removed")
	}
}

func TestCredentialControlRetriesPreparedOrphanDeletion(t *testing.T) {
	registry := t.TempDir()
	const name = "web/workspace-provider/00000000000000000000000000000000"
	storage := &memoryStore{
		values:    map[string]string{name: "orphan-secret"},
		deleteErr: errors.New("keyring unavailable"),
	}
	control := &Control{
		root: filepath.Join(registry, "workspace"), registry: registry,
		namespace: "workspace-provider",
		base:      Reference{Kind: "env", Name: "API_KEY"},
		keyring:   storage,
	}
	intent := recoveryIntent{
		Version: 1, OperationID: "operation",
		OldReference: control.base,
		NewReference: Reference{Kind: "keyring", Name: name},
		Phase:        "prepared",
	}
	if err := control.writeIntent(intent); err != nil {
		t.Fatal(err)
	}
	if err := control.reconcile(t.Context()); err == nil {
		t.Fatal("expected transient keyring deletion failure")
	}
	var retained recoveryIntent
	if err := readJSON(
		filepath.Join(control.intentDir(), "operation.json"),
		&retained,
	); err != nil {
		t.Fatal(err)
	}
	if retained.Phase != "prepared" {
		t.Fatalf("intent phase = %q", retained.Phase)
	}
	storage.deleteErr = nil
	if err := control.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, exists := storage.values[name]; exists {
		t.Fatal("orphan was not removed on retry")
	}
}

func TestCredentialControlNeverDeletesCustomWebKeyringName(t *testing.T) {
	registry := t.TempDir()
	storage := &memoryStore{values: map[string]string{
		"web/custom": "external-secret",
	}}
	control := &Control{
		root: filepath.Join(registry, "workspace"), registry: registry,
		namespace: "workspace-provider",
		base:      Reference{Kind: "keyring", Name: "web/custom"},
		keyring:   storage,
	}
	service := New(control.base, WithControl(control))
	if _, err := service.SetKeyring(t.Context(), "managed-secret"); err != nil {
		t.Fatal(err)
	}
	if err := control.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if storage.values["web/custom"] != "external-secret" {
		t.Fatal("custom web/ credential was deleted")
	}
}

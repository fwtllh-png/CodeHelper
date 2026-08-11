package cli

import (
	"path/filepath"
	"testing"
)

func TestSetCredentialSlotValidatesReferenceByKind(t *testing.T) {
	tests := []struct {
		name    string
		slot    CredentialSlot
		wantErr bool
	}{
		{
			name: "environment",
			slot: CredentialSlot{Name: "openai", Kind: "env", Env: "OPENAI_API_KEY"},
		},
		{
			name:    "invalid environment name",
			slot:    CredentialSlot{Name: "openai", Kind: "env", Env: "sk-value"},
			wantErr: true,
		},
		{
			name:    "mixed environment and reference",
			slot:    CredentialSlot{Name: "openai", Kind: "env", Env: "OPENAI_API_KEY", Ref: "entry"},
			wantErr: true,
		},
		{
			name: "relative credential file",
			slot: CredentialSlot{Name: "openai", Kind: "file", Ref: "providers/openai"},
		},
		{
			name:    "escaping credential file",
			slot:    CredentialSlot{Name: "openai", Kind: "file", Ref: "../openai"},
			wantErr: true,
		},
		{
			name: "keyring entry",
			slot: CredentialSlot{Name: "openai", Kind: "keyring", Ref: "openai-primary"},
		},
		{
			name:    "invalid keyring entry",
			slot:    CredentialSlot{Name: "openai", Kind: "keyring", Ref: "raw secret"},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "codehelper.toml")
			err := setCredentialSlot(configPath, test.slot)
			if (err != nil) != test.wantErr {
				t.Fatalf("setCredentialSlot() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

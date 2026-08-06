package tui

import (
	"encoding/json"
	"fmt"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	toml "github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m Model) sessionDir(id string) string {
	if m.dataDir == "" {
		return ""
	}
	if !strings.HasPrefix(id, "thread-") {
		id = "thread-" + id
	}
	return filepath.Join(m.dataDir, id)
}

func (m Model) sessionNew() Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/new requires data-dir")
		return m
	}
	id := fmt.Sprintf("thread-%d", time.Now().UnixNano())
	if err := os.MkdirAll(m.sessionDir(id), 0o700); err != nil {
		m = m.noteStatus("session:new error:" + err.Error())
		return m
	}
	m.session = id
	_ = m.activateSession(id)
	m = m.noteStatus("session:new " + id)
	m = m.noteRelayIfPresent()
	return m
}

func (m Model) sessionLoad(id string) Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/load requires data-dir")
		return m
	}
	dir := m.sessionDir(id)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {

		if snap, snapErr := ux.LoadSnapshot(m.dataDir, id); snapErr == nil {
			if snap.ThreadID != "" {
				m.session = snap.ThreadID
			} else {
				m.session = id
			}
			if snap.Provider != "" {
				m.provider = snap.Provider
			}
			if snap.Model != "" {
				m.modelID = snap.Model
			}
			m.parentFork = snap.ParentFork
			if snap.Mode == "plan" {
				m.mode = ModePlan
				m.toolMode = policy.ModePlan
			} else if snap.Mode == "operate" {
				m.toolMode = policy.ModeOperate
			} else if snap.Mode == "act" {
				m.toolMode = policy.ModeAct
			}
			m = m.applySnapshotSecurity(snap)
			m = m.syncSecurity()
			_ = m.activateSession(m.session)
			if host, ok := m.runtime.(*SessionHost); ok {
				host.SetThreadID(m.session)
			}
			m = m.noteStatus("session:load-snapshot " + m.session)
			m = m.noteRelayIfPresent()
			return m
		}
		m = m.noteStatus("session:load missing " + id)
		return m
	}
	m.session = filepath.Base(dir)
	_ = m.activateSession(m.session)
	if snap, err := ux.LoadSnapshot(m.dataDir, "session-local"); err == nil {
		if snap.Provider != "" {
			m.provider = snap.Provider
		}
		if snap.Model != "" {
			m.modelID = snap.Model
		}
		m.parentFork = snap.ParentFork
		m = m.applySnapshotSecurity(snap)
		m = m.syncSecurity()
	}
	if host, ok := m.runtime.(*SessionHost); ok {
		host.SetThreadID(m.session)
	}
	m = m.noteStatus("session:load " + m.session)
	m = m.noteRelayIfPresent()
	return m
}

func (m Model) sessionSave() Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/save requires data-dir")
		return m
	}
	dir := m.sessionDir(m.session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m = m.noteStatus("session:save error:" + err.Error())
		return m
	}
	meta := map[string]any{
		"session": m.session, "provider": m.provider, "model": m.modelID,
		"saved_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "session.json"), append(data, '\n'), 0o600); err != nil {
		m = m.noteStatus("session:save error:" + err.Error())
		return m
	}
	summary := m.statusSummary(16)
	_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot(summary))
	_ = m.activateSession(m.session)
	m = m.noteStatus("session:save " + m.session)
	return m
}

func (m Model) sessionFork(to string) Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/fork requires data-dir")
		return m
	}
	src := m.sessionDir(m.session)
	dstID := to
	if !strings.HasPrefix(dstID, "thread-") {
		dstID = "thread-" + dstID
	}
	dst := filepath.Join(m.dataDir, dstID)
	if err := os.MkdirAll(src, 0o700); err != nil {
		m = m.noteStatus("session:fork error:" + err.Error())
		return m
	}
	_ = os.WriteFile(filepath.Join(src, "marker"), []byte(m.session), 0o600)
	if err := copyTree(src, dst); err != nil {
		m = m.noteStatus("session:fork error:" + err.Error())
		return m
	}
	m.session = dstID
	m.parentFork = filepath.Base(src)
	_ = m.activateSession(dstID)
	_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot(nil))
	m = m.noteStatus("session:fork " + dstID)
	return m
}

func (m Model) sessionExport() Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/export requires data-dir")
		return m
	}
	path := filepath.Join(m.dataDir, m.session+".export.json")
	payload := map[string]any{
		"session": m.session, "provider": m.provider, "model": m.modelID,
		"status": m.statusSummary(64),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		m = m.noteStatus("session:export error:" + err.Error())
		return m
	}
	m = m.noteStatus("session:export " + path)
	return m
}

func (m Model) activateSession(id string) error {
	if m.dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dataDir, "active-thread"), []byte(id+"\n"), 0o600)
}

func (m Model) writeSettings() error {
	if m.configPath == "" {
		return nil
	}
	doc := map[string]any{}
	if data, err := os.ReadFile(m.configPath); err == nil {
		_ = toml.Unmarshal(data, &doc)
	}
	execution, _ := doc["execution"].(map[string]any)
	if execution == nil {
		execution = map[string]any{}
	}
	execution["provider"] = m.provider
	execution["model"] = m.modelID
	doc["execution"] = execution
	out, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(m.configPath, out, 0o600)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

func (m Model) openPicker(kind PickerKind) Model {
	m.mode = ModePicker
	m.picker = kind
	m.pickerIndex = 0
	switch kind {
	case PickerModel:
		m.pickerItems = modelIDs()
	case PickerProvider:
		m.pickerItems = providerIDs()
	case PickerSession:
		m.pickerItems = m.listSessions()
	}
	return m
}

func (m Model) listSessions() []string {
	items := []string{m.session}
	if m.dataDir == "" {
		items = append(items, "thread-alt")
		return uniqueStrings(items)
	}
	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return uniqueStrings(items)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "thread-") {
			items = append(items, entry.Name())
		}
	}
	return uniqueStrings(items)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func (m Model) snapshotGranular() map[string]string {
	out := map[string]string{}
	if m.granular.Sandbox != policy.SurfaceInherit {
		out["sandbox"] = string(m.granular.Sandbox)
	}
	if m.granular.Rules != policy.SurfaceInherit {
		out["rules"] = string(m.granular.Rules)
	}
	if m.granular.Skills != policy.SurfaceInherit {
		out["skills"] = string(m.granular.Skills)
	}
	if m.granular.MCP != policy.SurfaceInherit {
		out["mcp"] = string(m.granular.MCP)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m Model) applySnapshotSecurity(snap ux.Snapshot) Model {
	if snap.Posture == "suggest" || snap.Posture == "auto" || snap.Posture == "bypass" {
		m.posture = snap.Posture
	}
	if len(snap.Granular) == 0 {
		return m
	}
	if value, ok := parseSurfacePosture(snap.Granular["sandbox"]); ok {
		m.granular.Sandbox = value
	}
	if value, ok := parseSurfacePosture(snap.Granular["rules"]); ok {
		m.granular.Rules = value
	}
	if value, ok := parseSurfacePosture(snap.Granular["skills"]); ok {
		m.granular.Skills = value
	}
	if value, ok := parseSurfacePosture(snap.Granular["mcp"]); ok {
		m.granular.MCP = value
	}
	return m
}

func (m Model) sessionSnapshot(messages []string) ux.Snapshot {
	return ux.Snapshot{
		SessionID: "session-local", ThreadID: m.session,
		Provider: m.provider, Model: m.modelID, Mode: string(m.toolMode),
		Posture: m.posture, Granular: m.snapshotGranular(),
		Messages: messages, ParentFork: m.parentFork, UpdatedAt: time.Now().UTC(),
	}
}

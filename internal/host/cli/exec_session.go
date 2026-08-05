package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type execSessionState struct {
	ThreadID  string `json:"thread_id"`
	SessionID string `json:"session_id,omitempty"`
}

func resolveExecSession(
	dataDir, threadFlag, sessionFlag string, resume bool,
) (protocol.ThreadID, string, error) {
	threadFlag = strings.TrimSpace(threadFlag)
	sessionFlag = strings.TrimSpace(sessionFlag)
	if resume {
		if threadFlag == "" && strings.TrimSpace(dataDir) == "" {
			return "", "", fmt.Errorf("--resume/--continue requires --data-dir or --thread-id")
		}
		if threadFlag == "" {
			loaded, err := loadExecSession(dataDir)
			if err != nil {
				return "", "", err
			}
			threadFlag = loaded.ThreadID
			if sessionFlag == "" {
				sessionFlag = loaded.SessionID
			}
		}
	}
	if threadFlag == "" {
		generated, err := protocol.NewThreadID()
		if err != nil {
			return "", "", err
		}
		threadFlag = string(generated)
	}
	if sessionFlag == "" {
		sessionFlag = "session-local"
	}
	return protocol.ThreadID(threadFlag), sessionFlag, nil
}

func loadExecSession(dataDir string) (execSessionState, error) {
	path := filepath.Join(dataDir, "last-exec.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var state execSessionState
		if err := json.Unmarshal(data, &state); err != nil {
			return execSessionState{}, fmt.Errorf("read last-exec.json: %w", err)
		}
		if strings.TrimSpace(state.ThreadID) != "" {
			return state, nil
		}
	} else if !os.IsNotExist(err) {
		return execSessionState{}, err
	}
	active, err := os.ReadFile(filepath.Join(dataDir, "active-thread"))
	if err != nil {
		if os.IsNotExist(err) {
			return execSessionState{}, fmt.Errorf("no resumable thread under %s (missing last-exec.json/active-thread)", dataDir)
		}
		return execSessionState{}, err
	}
	threadID := strings.TrimSpace(string(active))
	if threadID == "" {
		return execSessionState{}, fmt.Errorf("active-thread is empty under %s", dataDir)
	}
	return execSessionState{ThreadID: threadID}, nil
}

func persistExecSession(dataDir string, threadID protocol.ThreadID, sessionID string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	state := execSessionState{ThreadID: string(threadID), SessionID: sessionID}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dataDir, "last-exec.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "active-thread"), []byte(string(threadID)+"\n"), 0o600)
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

type liveModelRow struct {
	ID string `json:"id"`
}

type liveModelsPayload struct {
	Data []liveModelRow `json:"data"`
}

func listLiveModels(providerID string) (map[string]any, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, fmt.Errorf("provider is required")
	}
	catalog := model.DefaultCatalog()
	provider, ok := catalog.Provider(providerID)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerID)
	}
	if fixture := strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_LIST_FIXTURE")); fixture != "" {
		data, err := os.ReadFile(fixture)
		if err != nil {
			return nil, err
		}
		ids, err := parseLiveModelIDs(data)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"provider": provider.ID, "source": "fixture", "live": true,
			"models": ids, "count": len(ids),
		}, nil
	}
	needsCred := provider.Credential.Kind == "env" && provider.Credential.Name != ""
	apiKey := ""
	if needsCred {
		apiKey = strings.TrimSpace(os.Getenv(provider.Credential.Name))
		if apiKey == "" {
			return nil, fmt.Errorf("missing credential env %s (fail-closed)", provider.Credential.Name)
		}
	}
	base := strings.TrimRight(provider.Endpoint, "/")
	url := base + "/models"
	if override := strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_LIST_URL")); override != "" {
		url = override
	}
	gate := &egress.Gate{Enforce: true}
	if !gate.AllowURL(url) && !gate.AllowURL(base) {
		return nil, fmt.Errorf("live list endpoint host cannot be granted")
	}
	client := egress.WrapClient(&http.Client{Timeout: 8 * time.Second}, gate)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("live list unreachable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("live list HTTP %d", resp.StatusCode)
	}
	ids, err := parseLiveModelIDs(body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"provider": provider.ID, "source": "live", "live": true,
		"models": ids, "count": len(ids),
		"endpoint_host": hostOnly(base),
	}, nil
}

func parseLiveModelIDs(data []byte) ([]string, error) {
	var payload liveModelsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode models payload: %w", err)
	}
	ids := make([]string, 0, len(payload.Data))
	for _, row := range payload.Data {
		if id := strings.TrimSpace(row.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

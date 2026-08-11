package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

type liveModelRow struct {
	ID string `json:"id"`
}

type liveModelsPayload struct {
	Data []liveModelRow `json:"data"`
}

// ListLiveModels performs an explicit provider credential validation through
// the provider's model-list endpoint. It returns only non-secret catalog data.
func ListLiveModels(
	ctx context.Context,
	providerID string,
	credentialOverride model.CredentialRef,
) (map[string]any, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, fmt.Errorf("provider is required")
	}
	catalog := model.DefaultCatalog()
	catalogProvider, ok := catalog.Provider(providerID)
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
			"provider": catalogProvider.ID, "source": "fixture", "live": true,
			"models": ids, "count": len(ids),
		}, nil
	}
	credential := catalogProvider.Credential
	if credentialOverride.Kind != "" || credentialOverride.Name != "" {
		credential = credentialOverride
	}
	apiKey := ""
	if credential.Kind != "" && credential.Name != "" {
		var err error
		apiKey, err = httpclient.DefaultCredentials().Resolve(ctx, credential)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve credential %s: %w", credential.Name, err,
			)
		}
	}
	base := strings.TrimRight(catalogProvider.Endpoint, "/")
	target := base + "/models"
	if override := strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_LIST_URL")); override != "" {
		target = override
	}
	gate := &egress.Gate{Enforce: true}
	if !gate.AllowURL(target) && !gate.AllowURL(base) {
		return nil, fmt.Errorf("live list endpoint host cannot be granted")
	}
	client := egress.WrapClient(&http.Client{Timeout: 8 * time.Second}, gate)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("live list unreachable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("live list HTTP %d", response.StatusCode)
	}
	ids, err := parseLiveModelIDs(body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"provider": catalogProvider.ID, "source": "live", "live": true,
		"models": ids, "count": len(ids),
		"endpoint_host": liveModelHost(base),
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

func liveModelHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

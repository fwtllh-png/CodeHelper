package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/router"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

var NewRegistry, New = router.NewRegistry, router.New

type liveModelRow struct {
	ID string `json:"id"`
}

func List(
	ctx context.Context,
	providerID, baseURL string,
	credentialOverride model.CredentialRef,
) (map[string]any, error) {
	providerID = strings.TrimSpace(providerID)
	catalogProvider, ok := model.DefaultCatalog().Provider(providerID)
	if !ok {
		if providerID == "" || strings.TrimSpace(baseURL) == "" {
			return nil, fmt.Errorf("unknown provider %q", providerID)
		}
		catalogProvider = model.Provider{ID: providerID, Endpoint: baseURL}
	}
	if fixture := strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_LIST_FIXTURE")); fixture != "" {
		data, err := os.ReadFile(fixture)
		if err != nil {
			return nil, err
		}
		return liveModelResult(catalogProvider.ID, "fixture", "", data)
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
			return nil, fmt.Errorf("resolve credential %s: %w", credential.Name, err)
		}
	}
	base := strings.TrimRight(catalogProvider.Endpoint, "/")
	if strings.TrimSpace(baseURL) != "" {
		base = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
	target := base + "/models"
	if override := strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_LIST_URL")); override != "" {
		target = override
	}
	gate := &egress.Gate{Enforce: true}
	if !gate.AllowURL(target) && !gate.AllowURL(base) {
		return nil, fmt.Errorf("live list endpoint host cannot be granted")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := egress.WrapClient(
		&http.Client{Timeout: 8 * time.Second},
		gate,
	).Do(request)
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
	return liveModelResult(catalogProvider.ID, "live", liveModelHost(base), body)
}

func Probe(
	ctx context.Context,
	providerID, baseURL string,
	credential model.CredentialRef,
	modelID string,
) (bool, error) {
	value, err := List(ctx, providerID, baseURL, credential)
	if err != nil {
		return false, err
	}
	ids, _ := value["models"].([]string)
	return slices.Contains(ids, strings.TrimSpace(modelID)), nil
}

func liveModelResult(provider, source, endpointHost string, data []byte) (map[string]any, error) {
	var payload struct {
		Data []liveModelRow `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode models payload: %w", err)
	}
	ids := make([]string, 0, len(payload.Data))
	for _, row := range payload.Data {
		if id := strings.TrimSpace(row.ID); id != "" {
			ids = append(ids, id)
		}
	}
	result := map[string]any{
		"provider": provider, "source": source, "live": true,
		"models": ids, "count": len(ids),
	}
	if endpointHost != "" {
		result["endpoint_host"] = endpointHost
	}
	return result, nil
}

func liveModelHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

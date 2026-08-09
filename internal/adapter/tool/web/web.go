package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"golang.org/x/net/html"
)

const (
	defaultBodyLimit = 32 << 20  // 32 MiB — large HTML / raw docs without thrashing
	maxBodyLimit     = 128 << 20 // hard cap even if the model asks for more
	// minBodyLimit floors tiny model-supplied caps. Agents often pass 50–120 KiB
	// "to be safe", then thrash on response_too_large for every real page.
	minBodyLimit   = 1 << 20
	maxRedirects   = 5
	defaultDDGURL  = "https://html.duckduckgo.com/html/"
	defaultBingURL = "https://www.bing.com/search"
)

var errRedirectLimit = errors.New("redirect limit exceeded")

type Tool struct {
	kind          string
	searchBackend string
	searchURL     string
	primaryURL    string
	fallbackURL   string
	tavilyURL     string
	tavilyAPIKey  string
	searxngURL    string
	bochaURL      string
	bochaAPIKey   string
	browser       BrowserRuntime
	httpClient    *http.Client
}

type Citation struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type input struct {
	Query     string   `json:"query"`
	Domains   []string `json:"domains"`
	Recency   string   `json:"recency"`
	URL       string   `json:"url"`
	Limit     int      `json:"limit"`
	MaxBytes  int64    `json:"max_bytes"`
	TimeoutMS int      `json:"timeout_ms"`
}

func Register(registry *tool.Registry, _ string) error {
	return RegisterWithOptions(registry, OptionsFromEnv())
}

func RegisterWithOptions(registry *tool.Registry, options Options) error {
	if options.PrimaryURL == "" {
		options.PrimaryURL = defaultDDGURL
	}
	if options.FallbackURL == "" {
		options.FallbackURL = defaultBingURL
	}
	if options.TavilyURL == "" {
		options.TavilyURL = defaultTavilyURL
	}
	if options.SearXNGURL == "" {
		options.SearXNGURL = defaultSearXNGURL
	}
	if options.BochaURL == "" {
		options.BochaURL = defaultBochaURL
	}
	for _, kind := range []string{"web_search", "web_fetch", "web_scrape"} {
		if err := registry.Register(&Tool{
			kind: kind, searchBackend: options.SearchBackend, searchURL: options.SearchURL,
			primaryURL: options.PrimaryURL, fallbackURL: options.FallbackURL,
			tavilyURL: options.TavilyURL, tavilyAPIKey: options.TavilyAPIKey,
			searxngURL: options.SearXNGURL, bochaURL: options.BochaURL, bochaAPIKey: options.BochaAPIKey,
			browser: options.Browser, httpClient: options.HTTP,
		}, nil); err != nil {
			return err
		}
	}
	return registerBrowser(registry, options.Browser)
}

func (t *Tool) Descriptor() tool.Descriptor {
	properties := map[string]any{
		"timeout_ms": map[string]any{"type": "integer"},
		"max_bytes": map[string]any{
			"type":        "integer",
			"description": "response byte cap (default 32MiB, max 128MiB; values below 1MiB are raised to the default)",
		},
	}
	required := []string{"url"}
	switch t.kind {
	case "web_search":
		properties = map[string]any{
			"query":      map[string]any{"type": "string", "minLength": 1},
			"limit":      map[string]any{"type": "integer"},
			"domains":    map[string]any{"type": "array"},
			"recency":    map[string]any{"type": "string", "enum": []any{"day", "week", "month", "year"}},
			"timeout_ms": map[string]any{"type": "integer"},
		}
		required = []string{"query"}
	default:
		properties["url"] = map[string]any{"type": "string", "minLength": 1}
	}
	description := map[string]string{
		"web_search": "Search the Web with selectable backends (DDG/Bing/Tavily/SearXNG/Bocha) and return normalized citations",
		"web_fetch":  "Fetch a bounded HTTP response with redirect and timeout limits",
		"web_scrape": "Fetch HTML and extract its title, readable text, and citation",
	}[t.kind]
	resolver := tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
		Kind: "url", Field: "url", Access: tool.AccessRead,
	}}}
	if t.kind == "web_search" {
		resolver = tool.ResourceResolver{Templates: t.searchResourceTemplates()}
	}
	return tool.Descriptor{
		Name: t.kind, Description: description, Visibility: tool.VisibleModel,
		Capability: tool.CapabilityNetwork, AccessMode: tool.AccessRead,
		ResourceResolver:   resolver,
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required,
			"additionalProperties": false,
		},
	}
}

// searchResourceTemplates lists the configured backend endpoints as url
// resources so Guard can approve hosts before Dial. The query string is not a
// network target and must not appear here.
func (t *Tool) searchResourceTemplates() []tool.ResourceTemplate {
	var endpoints []string
	switch strings.ToLower(strings.TrimSpace(t.searchBackend)) {
	case "tavily":
		endpoints = []string{t.tavilyURL}
	case "searxng":
		endpoints = []string{strings.TrimRight(t.searxngURL, "/") + "/search"}
	case "bocha":
		endpoints = []string{t.bochaURL}
	case "duckduckgo":
		endpoints = []string{t.primaryURL}
	case "bing":
		endpoints = []string{t.fallbackURL, "https://cn.bing.com/search"}
	case "custom":
		endpoints = []string{t.searchURL}
	default:
		// Include cn.bing.com: www.bing.com often redirects there and Gate
		// re-checks every redirect hop against the allowlist.
		endpoints = []string{t.primaryURL, t.fallbackURL, "https://cn.bing.com/search"}
	}
	out := make([]tool.ResourceTemplate, 0, len(endpoints))
	seen := map[string]struct{}{}
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		out = append(out, tool.ResourceTemplate{
			Kind: "url", ID: endpoint, Access: tool.AccessRead,
		})
	}
	return out
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var value input
	if err := json.Unmarshal(raw, &value); err != nil {
		return tool.Result{}, err
	}
	if t.kind == "web_search" {
		return t.search(ctx, value)
	}
	return t.fetch(ctx, value, t.kind == "web_scrape")
}

func (t *Tool) search(ctx context.Context, value input) (tool.Result, error) {
	limit := value.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	for _, domain := range value.Domains {
		if strings.TrimSpace(domain) == "" || strings.ContainsAny(domain, " \t\r\n") {
			return tool.Result{}, fmt.Errorf("invalid search domain %q", domain)
		}
	}

	backends, err := t.resolveBackends()
	if err != nil {
		return tool.Result{}, err
	}

	receipts := make([]map[string]any, 0, len(backends))
	var lastFailure tool.Result
	for index, backend := range backends {
		results, statusCode, failure, reason, err := searchBackendRequest(ctx, backend, value, limit, t.httpClient)
		if err != nil {
			return tool.Result{}, err
		}
		receipt := map[string]any{
			"backend": backend.name, "status_code": statusCode,
			"domains_applied": len(value.Domains) > 0,
			"recency_applied": value.Recency != "" && backend.supportsRecency,
		}
		if value.Recency != "" && !backend.supportsRecency {
			receipt["recency_unsupported"] = true
		}
		if failure != nil {
			receipt["outcome"] = "failed"
			receipt["reason"] = failure.Metadata["error_category"]
			lastFailure = *failure
		} else if reason != "" {
			receipt["outcome"] = reason
			receipt["reason"] = reason
		} else {
			receipt["outcome"] = "success"
		}
		receipts = append(receipts, receipt)
		if failure != nil || reason != "" || len(results) == 0 {
			if index < len(backends)-1 {
				continue
			}
			if failure != nil {
				lastFailure.Metadata["receipts"] = receipts
				return lastFailure, nil
			}
		}
		if len(results) > limit {
			results = results[:limit]
		}
		return encodeSearchResult(value.Query, results, backend.name, receipts, statusCode)
	}
	if lastFailure.Content == "" {
		lastFailure = webFailure("empty_results", 0, "Web search returned no results")
	}
	lastFailure.Metadata["receipts"] = receipts
	return lastFailure, nil
}

func encodeSearchResult(
	query string,
	results []searchResult,
	backend string,
	receipts []map[string]any,
	statusCode int,
) (tool.Result, error) {
	citations := make([]Citation, 0, len(results))
	for index, item := range results {
		citations = append(citations, Citation{
			ID: "web-" + strconv.Itoa(index+1), Title: item.Title, URL: item.URL, Snippet: item.Snippet,
		})
	}
	payload := map[string]any{
		"query": query, "results": results, "citations": citations,
		"backend": backend, "receipts": receipts,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"status_code": statusCode, "result_count": len(results), "citations": citations,
			"backend": backend, "receipts": receipts,
		},
	}, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func isSearchChallenge(body []byte) bool {
	value := strings.ToLower(string(body))
	return strings.Contains(value, "captcha") ||
		strings.Contains(value, "verify you are human") ||
		strings.Contains(value, "unusual traffic")
}

func parseSearchHTML(body []byte, format string) ([]searchResult, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	var results []searchResult
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			var container *html.Node
			switch format {
			case "duckduckgo":
				if hasClass(node, "result__a") {
					container = ancestorWithClass(node, "result")
				}
			case "bing":
				if ancestorElement(node, "h2") != nil {
					container = ancestorWithClass(node, "b_algo")
				}
			}
			if container != nil {
				href := attribute(node, "href")
				if format == "duckduckgo" {
					href = unwrapDuckDuckGoURL(href)
				}
				snippet := ""
				if format == "duckduckgo" {
					snippet = nodeText(descendantWithClass(container, "result__snippet"))
				} else {
					snippet = nodeText(descendantElement(container, "p"))
				}
				results = append(results, searchResult{
					Title: nodeText(node), URL: href, Snippet: snippet,
				})
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return results, nil
}

func unwrapDuckDuckGoURL(value string) string {
	parsed, err := url.Parse(value)
	if err == nil {
		if target := parsed.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return value
}

func hasClass(node *html.Node, class string) bool {
	for _, value := range strings.Fields(attribute(node, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func attribute(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func ancestorWithClass(node *html.Node, class string) *html.Node {
	for current := node.Parent; current != nil; current = current.Parent {
		if hasClass(current, class) {
			return current
		}
	}
	return nil
}

func ancestorElement(node *html.Node, element string) *html.Node {
	for current := node.Parent; current != nil; current = current.Parent {
		if current.Type == html.ElementNode && current.Data == element {
			return current
		}
	}
	return nil
}

func descendantWithClass(node *html.Node, class string) *html.Node {
	if node == nil {
		return nil
	}
	if hasClass(node, class) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if result := descendantWithClass(child, class); result != nil {
			return result
		}
	}
	return nil
}

func descendantElement(node *html.Node, element string) *html.Node {
	if node == nil {
		return nil
	}
	if node.Type == html.ElementNode && node.Data == element {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if result := descendantElement(child, element); result != nil {
			return result
		}
	}
	return nil
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var parts []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			parts = append(parts, current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return normalizeSpace(strings.Join(parts, " "))
}

func (t *Tool) fetch(ctx context.Context, value input, scrape bool) (tool.Result, error) {
	if !validHTTPURL(value.URL) {
		return tool.Result{}, errors.New("Web URL must use http or https without credentials")
	}
	limit := resolveBodyLimit(value.MaxBytes)
	body, response, failure, err := request(ctx, value.URL, limit, value.TimeoutMS, t.httpClient)
	if err != nil {
		return tool.Result{}, err
	}
	if failure != nil {
		return *failure, nil
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	truncated := false
	if int64(len(body)) > limit {
		body = body[:limit]
		truncated = true
	}
	metadata := map[string]any{
		"status_code": response.StatusCode, "final_url": response.Request.URL.String(),
		"content_type": contentType, "bytes": len(body),
	}
	if truncated {
		metadata["truncated"] = true
		metadata["byte_limit"] = limit
	}
	if !scrape {
		return tool.Result{Content: string(body), Truncated: truncated, Metadata: metadata}, nil
	}
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		return webFailure("unsupported_content_type", response.StatusCode, "Web scrape requires HTML"), nil
	}
	title, text, err := extractHTML(body)
	if err != nil {
		return webFailure("invalid_response", response.StatusCode, "invalid HTML document"), nil
	}
	citation := Citation{ID: "web-1", Title: title, URL: response.Request.URL.String()}
	payload := map[string]any{"title": title, "text": text, "citation": citation}
	content, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	metadata["citation"] = citation
	return tool.Result{Content: string(content), Truncated: truncated, Metadata: metadata}, nil
}

// resolveBodyLimit applies the default/hard caps. Sub-minBodyLimit requests are
// raised to the default: tiny caps produce empty failure loops on real pages.
func resolveBodyLimit(requested int64) int64 {
	if requested <= 0 || requested < minBodyLimit {
		return defaultBodyLimit
	}
	if requested > maxBodyLimit {
		return maxBodyLimit
	}
	return requested
}

func request(
	ctx context.Context,
	endpoint string,
	bodyLimit int64,
	timeoutMS int,
	httpClient *http.Client,
) ([]byte, *http.Response, *tool.Result, error) {
	timeout := 10 * time.Second
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,text/plain;q=0.8,*/*;q=0.7")
	req.Header.Set("User-Agent", "codehelper/web-fetch (+https://github.com/fwtllh-png/CodeHelper)")
	client := httpClient
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return errRedirectLimit
				}
				return nil
			},
		}
	} else if client.Timeout == 0 {
		// Clone so a shared egress-wrapped client is not mutated per call.
		clone := *client
		clone.Timeout = timeout
		if clone.CheckRedirect == nil {
			clone.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return errRedirectLimit
				}
				return nil
			}
		}
		client = &clone
	}
	response, err := client.Do(req)
	if err != nil {
		failure := httpTransportFailure(err, endpoint)
		return nil, nil, &failure, nil
	}
	defer response.Body.Close()
	// Read one past the limit so we can truncate at the call site (or refuse
	// only when even the hard max cannot hold the page).
	body, err := io.ReadAll(io.LimitReader(response.Body, bodyLimit+1))
	if err != nil {
		return nil, nil, nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		category := "http_error"
		message := strings.TrimSpace(string(body))
		switch {
		case response.StatusCode == http.StatusTooManyRequests:
			category = "rate_limited"
			message = "HTTP rate limited — wait before retrying; do not immediately re-fetch the same URL"
			if retry := strings.TrimSpace(response.Header.Get("Retry-After")); retry != "" {
				message += " (Retry-After: " + retry + ")"
			}
		case message == "" || hasHTMLDocumentSignature(message):
			message = fmt.Sprintf("HTTP %d %s", response.StatusCode, http.StatusText(response.StatusCode))
		case len(message) > 512:
			message = message[:512] + "…"
		}
		if response.StatusCode == http.StatusNotFound {
			message += " — do not retry this exact URL; pick a different path or source"
		}
		finalURL := endpoint
		if response.Request != nil && response.Request.URL != nil {
			finalURL = response.Request.URL.String()
		}
		message = fmt.Sprintf("%s · %s", message, finalURL)
		failure := webFailure(category, response.StatusCode, message)
		failure.Metadata["url"] = endpoint
		failure.Metadata["final_url"] = finalURL
		return nil, response, &failure, nil
	}
	if bodyLimit >= maxBodyLimit && int64(len(body)) > maxBodyLimit {
		failure := webFailure(
			"response_too_large", response.StatusCode,
			fmt.Sprintf(
				"Web response exceeds hard byte limit (%d bytes) · %s. Fetch a smaller page or a raw/content URL.",
				maxBodyLimit, endpoint,
			),
		)
		failure.Metadata["url"] = endpoint
		return nil, response, &failure, nil
	}
	return body, response, nil, nil
}

func hasHTMLDocumentSignature(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(trimmed, "<!doctype") ||
		strings.HasPrefix(trimmed, "<html") ||
		strings.Contains(trimmed[:min(len(trimmed), 256)], "<head")
}

func webFailure(category string, status int, message string) tool.Result {
	return tool.Result{
		Content: message, IsError: true,
		Metadata: map[string]any{"error_category": category, "status_code": status},
	}
}

// httpTransportFailure classifies Dial/RoundTrip errors. Egress policy denials
// are a distinct category so Guard can ask to Grant the host and retry; plain
// timeouts and connection failures stay non-approvable.
func httpTransportFailure(err error, requestURL string) tool.Result {
	category := "network"
	message := err.Error()
	meta := map[string]any{"error_category": category, "status_code": 0}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		category = "timeout"
		meta["error_category"] = category
	case errors.Is(err, egress.ErrDenied):
		category = "egress_denied"
		meta["error_category"] = category
		host, protocol := hostFromDeniedRequest(err, requestURL)
		if host != "" {
			meta["host"] = host
			meta["protocol"] = protocol
			message = fmt.Sprintf("egress denied · host=%s", host)
		} else {
			message = "egress denied"
		}
	case errors.Is(err, errRedirectLimit):
		category = "redirect_limit"
		meta["error_category"] = category
	}
	return tool.Result{Content: message, IsError: true, Metadata: meta}
}

func hostFromDeniedRequest(err error, requestURL string) (host, protocol string) {
	if host, protocol, ok := egress.DeniedTarget(err); ok {
		return host, protocol
	}
	protocol = "https"
	if requestURL != "" {
		if parsed, parseErr := url.Parse(requestURL); parseErr == nil {
			if h, p, ok := egress.HostOf(parsed); ok {
				return h, p
			}
		}
	}
	return host, protocol
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Host != "" && parsed.User == nil
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func extractHTML(body []byte) (string, string, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	var title string
	var parts []string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, ignored bool) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "svg":
				ignored = true
			}
		}
		if node.Type == html.TextNode && !ignored {
			value := strings.TrimSpace(node.Data)
			if value != "" {
				if node.Parent != nil && node.Parent.Data == "title" {
					title = normalizeSpace(value)
				} else {
					parts = append(parts, value)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, ignored)
		}
	}
	walk(document, false)
	return title, normalizeSpace(strings.Join(parts, " ")), nil
}

func normalizeSpace(value string) string {
	return strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
}

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOAuthPKCECallbackAndRefresh(t *testing.T) {
	var mu sync.Mutex
	challenge := ""
	tokenCalls := 0
	var authorizationServer *httptest.Server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		tokenCalls++
		switch request.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				t.Errorf("PKCE verifier did not match challenge")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token":  "initial-access",
				"refresh_token": "refresh-secret",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case "refresh_token":
			if request.Form.Get("refresh_token") != "refresh-secret" {
				t.Errorf("unexpected refresh token")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"access_token": "refreshed-access",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		default:
			t.Errorf("unexpected grant type %q", request.Form.Get("grant_type"))
		}
	}))
	defer tokenServer.Close()
	authorizationServer = httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		mu.Lock()
		challenge = request.URL.Query().Get("code_challenge")
		mu.Unlock()
		redirect, err := url.Parse(request.URL.Query().Get("redirect_uri"))
		if err != nil {
			t.Errorf("parse redirect: %v", err)
			return
		}
		query := redirect.Query()
		query.Set("code", "fixture-code")
		query.Set("state", request.URL.Query().Get("state"))
		redirect.RawQuery = query.Encode()
		http.Redirect(writer, request, redirect.String(), http.StatusFound)
	}))
	defer authorizationServer.Close()

	store := &memoryTokenStore{}
	client := &http.Client{Timeout: 2 * time.Second}
	manager, err := NewOAuthManager(OAuthConfig{
		AuthorizationEndpoint: authorizationServer.URL,
		TokenEndpoint:         tokenServer.URL,
		ClientID:              "fixture-client",
		CallbackTimeout:       2 * time.Second,
	}, client, store, BrowserOpenFunc(func(ctx context.Context, target string) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := manager.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer initial-access" {
		t.Fatalf("authorization = %q", authorization)
	}
	store.mu.Lock()
	store.token.Expiry = time.Now().Add(-time.Minute)
	store.mu.Unlock()
	authorization, err = manager.Authorization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer refreshed-access" {
		t.Fatalf("refreshed authorization = %q", authorization)
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenCalls != 2 {
		t.Fatalf("token calls = %d", tokenCalls)
	}
}

func TestFileTokenStoreUsesOpaqueRestrictedKeys(t *testing.T) {
	directory := t.TempDir()
	store, err := NewFileTokenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	key := "https://authorization.invalid/path?secret=value"
	token := OAuthToken{
		AccessToken:  "raw-access-token",
		RefreshToken: "raw-refresh-token",
	}
	if err := store.Save(context.Background(), key, token); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("token files = %d", len(entries))
	}
	name := entries[0].Name()
	if strings.Contains(name, "authorization") ||
		strings.Contains(name, "secret") ||
		strings.Contains(name, "token") {
		t.Fatalf("token store key leaked identity: %q", name)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o", info.Mode().Perm())
	}
	loaded, err := store.Load(context.Background(), key)
	if err != nil || loaded.AccessToken != token.AccessToken {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

type memoryTokenStore struct {
	mu    sync.Mutex
	token OAuthToken
	set   bool
}

func (s *memoryTokenStore) Load(context.Context, string) (OAuthToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set {
		return OAuthToken{}, ErrTokenNotFound
	}
	return s.token, nil
}

func (s *memoryTokenStore) Save(_ context.Context, _ string, token OAuthToken) error {
	s.mu.Lock()
	s.token = token
	s.set = true
	s.mu.Unlock()
	return nil
}

func (s *memoryTokenStore) Delete(context.Context, string) error {
	s.mu.Lock()
	s.token = OAuthToken{}
	s.set = false
	s.mu.Unlock()
	return nil
}

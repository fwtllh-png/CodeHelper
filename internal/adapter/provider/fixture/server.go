package fixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

type Config struct {
	Protocol                 model.WireProtocol `json:"protocol"`
	Path                     string             `json:"path"`
	Model                    string             `json:"model"`
	ExpectedPrompt           string             `json:"expected_prompt,omitempty"`
	ExpectedRequestFragments [][]string         `json:"expected_request_fragments,omitempty"`
	Streams                  []string           `json:"streams,omitempty"`
	StreamDelayMS            int                `json:"stream_delay_ms,omitempty"`
}

type Server struct {
	URL      string
	Config   Config
	server   *http.Server
	listener net.Listener
	done     chan error
}

func Start(directory string) (*Server, error) {
	configData, err := os.ReadFile(filepath.Join(directory, "fixture.json"))
	if err != nil {
		return nil, fmt.Errorf("read provider fixture config: %w", err)
	}
	var config Config
	decoder := json.NewDecoder(strings.NewReader(string(configData)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode provider fixture config: %w", err)
	}
	if config.Path == "" || config.Path[0] != '/' || config.Model == "" {
		return nil, errors.New("provider fixture path and model are required")
	}
	if config.StreamDelayMS < 0 {
		return nil, errors.New("provider fixture stream_delay_ms must not be negative")
	}
	switch config.Protocol {
	case model.ProtocolOpenAIChat, model.ProtocolOpenAIResponses, model.ProtocolAnthropic:
	default:
		return nil, fmt.Errorf("unsupported provider fixture protocol %q", config.Protocol)
	}
	streamNames := config.Streams
	if len(streamNames) == 0 {
		streamNames = []string{"stream.sse"}
	}
	streamData := make([][]byte, 0, len(streamNames))
	for _, name := range streamNames {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("read provider fixture stream: %w", err)
		}
		streamData = append(streamData, data)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for provider fixture: %w", err)
	}
	result := &Server{
		URL:      "http://" + listener.Addr().String(),
		Config:   config,
		listener: listener,
		done:     make(chan error, 1),
	}
	mux := http.NewServeMux()
	var requestIndex atomic.Uint64
	mux.HandleFunc(config.Path, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "POST required", http.StatusMethodNotAllowed)
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if readErr != nil {
			http.Error(writer, "read request", http.StatusBadRequest)
			return
		}
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil || payload["model"] != config.Model || payload["stream"] != true {
			http.Error(writer, "unexpected provider request", http.StatusBadRequest)
			return
		}
		if config.ExpectedPrompt != "" && !strings.Contains(string(body), config.ExpectedPrompt) {
			http.Error(writer, "expected prompt missing", http.StatusBadRequest)
			return
		}
		index := int(requestIndex.Add(1) - 1)
		if index >= len(streamData) {
			http.Error(writer, "provider fixture streams exhausted", http.StatusConflict)
			return
		}
		if index < len(config.ExpectedRequestFragments) {
			for _, fragment := range config.ExpectedRequestFragments[index] {
				if !strings.Contains(string(body), fragment) {
					http.Error(writer, "expected request fragment missing: "+fragment, http.StatusBadRequest)
					return
				}
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		for _, line := range strings.SplitAfter(string(streamData[index]), "\n") {
			if config.StreamDelayMS > 0 {
				time.Sleep(time.Duration(config.StreamDelayMS) * time.Millisecond)
			}
			if _, writeErr := io.WriteString(writer, line); writeErr != nil {
				return
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	})
	result.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		result.done <- result.server.Serve(listener)
	}()
	return result, nil
}

func (s *Server) Close(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	shutdownErr := s.server.Shutdown(ctx)
	select {
	case serveErr := <-s.done:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(shutdownErr, serveErr)
		}
	default:
	}
	return shutdownErr
}

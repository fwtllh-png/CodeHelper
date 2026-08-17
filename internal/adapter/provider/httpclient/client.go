package httpclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/observability/providerdump"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/tracecontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

const maxErrorBodyBytes = 16 << 10

var requestSequence atomic.Uint64

type CredentialResolver interface {
	Resolve(context.Context, model.CredentialRef) (string, error)
}
type Client struct {
	HTTP              *http.Client
	Credentials       CredentialResolver
	Egress            *egress.Gate
	Metrics           *telemetry.Metrics
	IdleTimeout       time.Duration
	MaxConcurrent     int
	RequestsPerSecond float64

	mu          sync.Mutex
	active      int
	nextRequest time.Time
	health      Health
}
type Health struct {
	Healthy             bool      `json:"healthy"`
	Active              int       `json:"active"`
	ConsecutiveFailures uint64    `json:"consecutive_failures"`
	LastError           string    `json:"last_error,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func New() *Client {
	return &Client{
		HTTP:          &http.Client{},
		Credentials:   DefaultCredentials(),
		IdleTimeout:   60 * time.Second,
		MaxConcurrent: 8,
		health:        Health{Healthy: true, UpdatedAt: time.Now()},
	}
}
func (c *Client) httpClient() *http.Client {
	base := c.HTTP
	if base == nil {
		base = http.DefaultClient
	}
	if c.Egress == nil {
		return base
	}
	return egress.WrapClient(base, c.Egress)
}
func (c *Client) Execute(
	ctx context.Context,
	request provider.ModelRequest,
	call providerwire.PreparedCall,
	adapter providerwire.Adapter,
) (provider.Stream, error) {
	requestContext, requestCancel, credential, err := c.begin(ctx, request.Route)
	if err != nil {
		return nil, err
	}
	release := true
	defer func() {
		if release {
			requestCancel()
			c.release()
		}
	}()
	httpClient := c.httpClient()
	httpRequest, err := http.NewRequestWithContext(
		requestContext,
		call.Method,
		joinEndpoint(request.Route.Endpoint(), call.Path),
		bytes.NewReader(call.Body),
	)
	if err != nil {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument, "create provider request", false, err,
		)
	}
	applyHeaders(httpRequest, call, credential)
	tracecontext.InjectHTTP(requestContext, httpRequest.Header)
	httpRequest.Header.Set("Idempotency-Key", requestKey(call.Body))
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		problem := protocol.NewProblem(
			protocol.CodeUnavailable,
			"provider request failed",
			retryableTransportError(err),
			err,
		)
		c.recordFailure(problem)
		return nil, problem
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		stream, err := adapter.OpenStream(response.Body, call)
		if err != nil {
			_ = response.Body.Close()
			requestCancel()
			c.recordFailure(err)
			return nil, err
		}
		release = false
		return c.wrapStream(
			stream,
			providerwire.MetadataWithProjection(call.Body, call.Body, false, call.Projection),
			requestCancel,
		), nil
	}
	errorText := boundedBody(response.Body)
	problem := adapter.ClassifyHTTP(providerwire.HTTPFailure{
		Status: response.StatusCode, Header: response.Header, Body: errorText,
	})
	if providerdump.Enabled(response.StatusCode) {
		if dumpPath, dumpErr := providerdump.Write(
			request, call.Body, call.Path, response.StatusCode, errorText,
		); dumpErr == nil && dumpPath != "" {
			if typed, ok := problem.(*protocol.Problem); ok {
				typed.Message += " [diagnostic: " + dumpPath + "]"
			}
		}
	}
	c.recordFailure(problem)
	return nil, problem
}
func (c *Client) begin(
	ctx context.Context,
	route model.ReadyRoute,
) (context.Context, context.CancelFunc, string, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, nil, "", err
	}
	requestContext, cancel := context.WithCancel(ctx)
	fail := func(err error) (context.Context, context.CancelFunc, string, error) {
		cancel()
		c.release()
		return nil, nil, "", err
	}
	if err := c.rateLimit(requestContext); err != nil {
		return fail(err)
	}
	resolver := c.Credentials
	if resolver == nil {
		resolver = DefaultCredentials()
	}
	credential, err := resolver.Resolve(requestContext, route.Credential())
	if err != nil {
		return fail(protocol.NewProblem(
			protocol.CodeUnavailable, "resolve provider credential", false, err,
		))
	}
	return requestContext, cancel, credential, nil
}

type managedStream struct {
	stream      provider.Stream
	cancel      context.CancelFunc
	release     func()
	idleTimeout time.Duration
	success     func()
	failure     func(error)
	closeOnce   sync.Once
}

func (s *managedStream) TransportMetadata() provider.TransportMetadata {
	return provider.Metadata(s.stream)
}

type metadataStream struct {
	provider.Stream
	metadata provider.TransportMetadata
}

func (s *metadataStream) TransportMetadata() provider.TransportMetadata {
	return s.metadata
}
func (s *metadataStream) Recv() (provider.StreamEvent, error) {
	event, err := s.Stream.Recv()
	if event.Usage != nil {
		event.Usage.Transport = s.metadata
	}
	return event, err
}

type receiveResult struct {
	event provider.StreamEvent
	err   error
}

func (s *managedStream) Recv() (provider.StreamEvent, error) {
	if s.idleTimeout <= 0 {
		event, err := s.stream.Recv()
		err = normalizeStreamError(err)
		s.observe(event, err)
		return event, err
	}
	result := make(chan receiveResult, 1)
	go func() {
		event, err := s.stream.Recv()
		result <- receiveResult{event: event, err: err}
	}()
	timer := time.NewTimer(s.idleTimeout)
	defer timer.Stop()
	select {
	case value := <-result:
		err := normalizeStreamError(value.err)
		s.observe(value.event, err)
		return value.event, err
	case <-timer.C:
		err := protocol.NewProblem(
			protocol.CodeUnavailable,
			fmt.Sprintf("provider stream idle timeout after %s", s.idleTimeout),
			true,
			context.DeadlineExceeded,
		)
		s.failure(err)
		s.cancel()
		<-result
		_ = s.Close()
		return provider.StreamEvent{}, err
	}
}
func normalizeStreamError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || protocol.CodeOf(err) != protocol.CodeInternal {
		return err
	}
	if retryableTransportError(err) {
		return protocol.NewProblem(
			protocol.CodeUnavailable,
			"provider stream transport failed",
			true,
			err,
		)
	}
	return err
}
func (s *managedStream) observe(event provider.StreamEvent, err error) {
	if err != nil && !errors.Is(err, io.EOF) {
		s.failure(err)
	}
	if event.Type == provider.EventMessageStop {
		s.success()
	}
	if err != nil || event.Type == provider.EventMessageStop {
		_ = s.Close()
	}
}
func (s *managedStream) Close() (result error) {
	s.closeOnce.Do(func() {
		s.cancel()
		result = s.stream.Close()
		s.release()
	})
	return result
}
func (c *Client) acquire(ctx context.Context) error {
	for {
		c.mu.Lock()
		maximum := c.MaxConcurrent
		if maximum <= 0 {
			maximum = 8
		}
		if c.active < maximum {
			c.active++
			c.health.Active = c.active
			c.health.UpdatedAt = time.Now()
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
func (c *Client) release() {
	c.mu.Lock()
	if c.active > 0 {
		c.active--
	}
	c.health.Active = c.active
	c.health.UpdatedAt = time.Now()
	c.mu.Unlock()
}
func (c *Client) rateLimit(ctx context.Context) error {
	c.mu.Lock()
	rate := c.RequestsPerSecond
	if rate <= 0 {
		c.mu.Unlock()
		return nil
	}
	now := time.Now()
	waitFor := c.nextRequest.Sub(now)
	if waitFor < 0 {
		waitFor = 0
	}
	c.nextRequest = now.Add(waitFor + time.Duration(float64(time.Second)/rate))
	c.mu.Unlock()
	return wait(ctx, waitFor)
}
func (c *Client) recordSuccess() {
	c.mu.Lock()
	c.health.Healthy = true
	c.health.ConsecutiveFailures = 0
	c.health.LastError = ""
	c.health.UpdatedAt = time.Now()
	c.mu.Unlock()
}
func (c *Client) recordFailure(err error) {
	c.mu.Lock()
	c.health.ConsecutiveFailures++
	c.health.Healthy = c.health.ConsecutiveFailures < 3
	c.health.LastError = errorString(err)
	c.health.UpdatedAt = time.Now()
	c.mu.Unlock()
}
func (c *Client) Health() Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func joinEndpoint(endpoint, path string) string {
	return strings.TrimRight(endpoint, "/") + path
}
func boundedBody(body io.ReadCloser) string {
	defer body.Close()
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	return strings.TrimSpace(string(data))
}
func retryableTransportError(err error) bool {
	var certificateError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &certificateError) ||
		errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &recordHeaderError) {
		return false
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return !dnsError.IsNotFound && (dnsError.IsTimeout || dnsError.IsTemporary)
	}
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}
func requestKey(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("codehelper-%x-%d", digest[:8], requestSequence.Add(1))
}
func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ providerwire.Transport = (*Client)(nil)

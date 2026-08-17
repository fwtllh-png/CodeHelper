package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type ManagedNetworkProxy struct {
	gate     *Gate
	listener net.Listener
	server   *http.Server
	done     chan struct{}
}

func StartManagedNetworkProxy(gate *Gate) (*ManagedNetworkProxy, error) {
	if gate == nil || !gate.Enforce {
		return nil, errors.New("managed network proxy requires an enforcing egress gate")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen managed network proxy: %w", err)
	}
	proxy := &ManagedNetworkProxy{
		gate: gate, listener: listener, done: make(chan struct{}),
	}
	proxy.server = &http.Server{
		Handler:           http.HandlerFunc(proxy.serveHTTP),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		_ = proxy.server.Serve(listener)
		close(proxy.done)
	}()
	return proxy, nil
}

func NewManagedBackend(
	gate *Gate,
	options sandbox.Options,
	build func(sandbox.Options) (sandbox.Backend, error),
) (sandbox.Backend, error) {
	if build == nil {
		return nil, errors.New("managed network backend factory is required")
	}
	proxy, err := StartManagedNetworkProxy(gate)
	if err != nil {
		return nil, err
	}
	options.AllowNetwork = false
	options.ManagedProxyPort = sandbox.ManagedNetworkProxyPort(proxy.Port())
	backend, err := build(options)
	if err != nil {
		_ = proxy.Close(context.Background())
		return nil, fmt.Errorf("create managed sandbox backend: %w", err)
	}
	return sandbox.WithClose(backend, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return proxy.Close(ctx)
	}), nil
}

func (p *ManagedNetworkProxy) URL() string {
	if p == nil || p.listener == nil {
		return ""
	}
	return "http://" + p.listener.Addr().String()
}

func (p *ManagedNetworkProxy) Port() uint16 {
	if p == nil || p.listener == nil {
		return 0
	}
	address, _ := p.listener.Addr().(*net.TCPAddr)
	if address == nil || address.Port < 1 || address.Port > 65535 {
		return 0
	}
	return uint16(address.Port)
}

func (p *ManagedNetworkProxy) Close(ctx context.Context) error {
	if p == nil || p.server == nil {
		return nil
	}
	err := p.server.Shutdown(ctx)
	select {
	case <-p.done:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

func (p *ManagedNetworkProxy) serveHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method == http.MethodConnect {
		p.serveConnect(writer, request)
		return
	}
	p.serveForward(writer, request)
}

func (p *ManagedNetworkProxy) serveConnect(
	writer http.ResponseWriter,
	request *http.Request,
) {
	host, port, err := splitAuthority(request.Host, 443)
	if err != nil {
		http.Error(writer, "invalid CONNECT target", http.StatusBadRequest)
		return
	}
	target, err := p.dialAuthorized(request.Context(), Target{
		Host: host, Protocol: "https", Port: port,
		Methods: []string{http.MethodConnect},
	})
	if err != nil {
		http.Error(writer, "managed egress denied", http.StatusForbidden)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		target.Close()
		http.Error(writer, "CONNECT is unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		target.Close()
		return
	}
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
		err = buffered.Flush()
	}
	if err != nil {
		client.Close()
		target.Close()
		return
	}
	go relay(client, target)
}

func (p *ManagedNetworkProxy) serveForward(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL == nil || request.URL.Hostname() == "" {
		http.Error(writer, "absolute proxy URL is required", http.StatusBadRequest)
		return
	}
	if request.Host != "" &&
		!sameAuthority(request.Host, request.URL.Host, request.URL.Scheme) {
		http.Error(writer, "proxy host mismatch", http.StatusBadRequest)
		return
	}
	port, err := requestPort(request.URL)
	if err != nil {
		http.Error(writer, "invalid target port", http.StatusBadRequest)
		return
	}
	ips, err := p.gate.Authorize(request.Context(), Target{
		Host: request.URL.Hostname(), Protocol: request.URL.Scheme, Port: port,
		Methods: []string{request.Method},
	}, "process_proxy")
	if err != nil {
		http.Error(writer, "managed egress denied", http.StatusForbidden)
		return
	}
	outbound := request.Clone(request.Context())
	outbound.RequestURI = ""
	removeHopHeaders(outbound.Header)
	transport := &http.Transport{
		Proxy:             nil,
		DialContext:       pinnedDialer(ips, request.URL.Hostname(), port),
		ForceAttemptHTTP2: false,
	}
	response, err := transport.RoundTrip(outbound)
	if err != nil {
		http.Error(writer, "managed egress upstream failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	removeHopHeaders(response.Header)
	for name, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func (p *ManagedNetworkProxy) dialAuthorized(
	ctx context.Context,
	target Target,
) (net.Conn, error) {
	ips, err := p.gate.Authorize(ctx, target, "process_proxy")
	if err != nil {
		return nil, err
	}
	return dialResolved(ctx, ips, target.Port)
}

func pinnedDialer(
	ips []net.IP,
	host string,
	port uint16,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		if len(ips) == 0 {
			return nil, fmt.Errorf("no approved address for %s", host)
		}
		return dialResolved(ctx, ips, port)
	}
}

func dialResolved(ctx context.Context, ips []net.IP, port uint16) (net.Conn, error) {
	var failures []error
	dialer := net.Dialer{}
	for _, ip := range ips {
		conn, err := dialer.DialContext(
			ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(port))),
		)
		if err == nil {
			return conn, nil
		}
		failures = append(failures, err)
	}
	return nil, errors.Join(failures...)
}

func splitAuthority(value string, fallback uint16) (string, uint16, error) {
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			return strings.Trim(value, "[]"), fallback, nil
		}
		return "", 0, err
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return "", 0, errors.New("invalid authority port")
	}
	return strings.Trim(host, "[]"), uint16(port), nil
}

func sameAuthority(header, target, protocol string) bool {
	left, leftPort, err := splitAuthority(header, defaultPort(protocol))
	if err != nil {
		return false
	}
	right, rightPort, err := splitAuthority(target, defaultPort(protocol))
	return err == nil && strings.EqualFold(left, right) && leftPort == rightPort
}

func defaultPort(protocol string) uint16 {
	if strings.EqualFold(protocol, "http") {
		return 80
	}
	return 443
}

func removeHopHeaders(header http.Header) {
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive",
		"Proxy-Authenticate", "Proxy-Authorization", "TE",
		"Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
}

func relay(left, right net.Conn) {
	defer left.Close()
	defer right.Close()
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(left, right)
	}()
	go func() {
		defer wait.Done()
		_, _ = io.Copy(right, left)
	}()
	wait.Wait()
}

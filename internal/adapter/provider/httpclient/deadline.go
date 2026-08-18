package httpclient

import (
	"net"
	"net/http"
	"time"
)

type DeadlineConfig struct {
	Connection      time.Duration
	TLSHandshake    time.Duration
	ResponseHeaders time.Duration
}

func withConnectionTimeout(
	client *Client,
	timeout time.Duration,
) *Client {
	client.SetConnectionTimeout(timeout)
	return client
}

// SetConnectionTimeout configures only connection establishment, TLS
// negotiation, and response headers. It deliberately leaves http.Client.Timeout
// unset because that wall-clock deadline also covers a healthy streaming body.
func (c *Client) SetConnectionTimeout(timeout time.Duration) {
	c.SetDeadlineConfig(DeadlineConfig{
		Connection: timeout, TLSHandshake: timeout, ResponseHeaders: timeout,
	})
}

func (c *Client) SetDeadlineConfig(config DeadlineConfig) {
	c.deadlines = config
	if c.HTTP == nil {
		c.HTTP = &http.Client{}
	}
	c.HTTP.Timeout = 0
	base := c.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	transport, ok := base.(*http.Transport)
	if !ok {
		return
	}
	clone := transport.Clone()
	if config.Connection > 0 {
		clone.DialContext = (&net.Dialer{
			Timeout:   config.Connection,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}
	clone.TLSHandshakeTimeout = config.TLSHandshake
	clone.ResponseHeaderTimeout = config.ResponseHeaders
	c.HTTP.Transport = clone
}

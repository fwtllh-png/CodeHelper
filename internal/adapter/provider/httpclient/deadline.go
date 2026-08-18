package httpclient

import (
	"net"
	"net/http"
	"time"
)

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
	if timeout > 0 {
		clone.DialContext = (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
		clone.TLSHandshakeTimeout = timeout
		clone.ResponseHeaderTimeout = timeout
	}
	c.HTTP.Transport = clone
}

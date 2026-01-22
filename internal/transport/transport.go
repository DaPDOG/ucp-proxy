// Package transport provides HTTP transport implementations for the proxy.
package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// =============================================================================
// TLS FINGERPRINT TRANSPORT
// =============================================================================
//
// Go's standard TLS client has a distinctive fingerprint that triggers
// aggressive rate limiting on some CDNs/platforms.
//
// This transport uses uTLS to present a Chrome-like TLS fingerprint with
// full HTTP/2 support. The approach:
//
//   1. Use uTLS with HelloChrome_Auto for Chrome's TLS fingerprint
//   2. Let ALPN negotiate naturally (h2, http/1.1)
//   3. Use Go's http2.Transport for HTTP/2 framing when negotiated
//
// =============================================================================

// NewChromeTransport creates an http.RoundTripper that presents Chrome's TLS
// fingerprint to upstream servers. Supports both HTTP/2 and HTTP/1.1 based on
// ALPN negotiation. Use this when targeting services behind CDNs that use
// JA3 fingerprinting for bot detection.
func NewChromeTransport(timeout time.Duration) http.RoundTripper {
	dialer := &net.Dialer{Timeout: timeout}

	// HTTP/2 transport with custom TLS dial
	h2Transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialChromeTLS(ctx, dialer, network, addr)
		},
	}

	// HTTP/1.1 fallback transport with custom TLS dial
	h1Transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialChromeTLS(ctx, dialer, network, addr)
		},
		ForceAttemptHTTP2: false,
	}

	return &chromeTransport{
		h2: h2Transport,
		h1: h1Transport,
	}
}

// chromeTransport wraps HTTP/2 and HTTP/1.1 transports with Chrome TLS fingerprint.
type chromeTransport struct {
	h2 *http2.Transport
	h1 *http.Transport
}

// RoundTrip implements http.RoundTripper.
// Tries HTTP/2 first, falls back to HTTP/1.1 if server doesn't support h2.
func (t *chromeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Try HTTP/2 first
	resp, err := t.h2.RoundTrip(req)
	if err == nil {
		return resp, nil
	}

	// Fall back to HTTP/1.1 if HTTP/2 fails
	// This handles servers that don't support HTTP/2
	return t.h1.RoundTrip(req)
}

// dialChromeTLS establishes a TLS connection with Chrome's fingerprint.
func dialChromeTLS(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	// Extract hostname for SNI
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	// Use Chrome fingerprint with default ALPN (h2, http/1.1)
	tlsConfig := &utls.Config{
		ServerName: host,
	}
	tlsConn := utls.UClient(conn, tlsConfig, utls.HelloChrome_Auto)

	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}

	return tlsConn, nil
}

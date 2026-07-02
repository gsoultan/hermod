package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// DefaultClient is a pre-configured secure and performant HTTP client.
var DefaultClient = NewSecureClient(10 * time.Second)

// NewSecureClient returns an http.Client with reasonable timeouts and basic
// SSRF protection by preventing connections to local/private IP ranges.
func NewSecureClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   SafeDialer,
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// Fetch performs a simple GET request with the default secure client.
func Fetch(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return DefaultClient.Do(req)
}

// IsIPPrivate reports whether the given IP address is in a private/local range.
func IsIPPrivate(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// IPv4 private ranges
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168)
	}

	// IPv6 private ranges (Unique Local Address)
	return len(ip) == 16 && ip[0] == 0xfc || ip[0] == 0xfd
}

// SafeDialer returns a dialer control function that rejects private IP addresses.
func SafeDialer(network, address string, c syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return err
	}

	for _, ip := range ips {
		if IsIPPrivate(ip) {
			return fmt.Errorf("connection to private IP %s is blocked", ip.String())
		}
	}
	return nil
}

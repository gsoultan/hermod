// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package rediskv

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"github.com/redis/go-redis/v9"
)

// defaultPort is the port Redis listens on unless redis.conf says otherwise.
const defaultPort = "6379"

// ParseConn reads where a Redis node connects from its config: addr, username,
// password, db and tls. addr is host:port, a bare host (port 6379), or a
// redis:// or rediss:// URL; a field that is set wins over the URL.
//
// An empty addr is an error. Passed on, go-redis would dial localhost:6379
// and the user would read "connection refused" for a server they never named.
func ParseConn(get func(key string) string) (Conn, error) {
	raw := strings.TrimSpace(get("addr"))
	if raw == "" {
		return Conn{}, errors.New("the address is required, as host:port (for example redis.internal:6379) or a redis:// URL")
	}
	var c Conn
	if strings.HasPrefix(raw, "redis://") || strings.HasPrefix(raw, "rediss://") {
		opts, err := redis.ParseURL(raw)
		if err != nil {
			// A *url.Error quotes the whole URL, password included, and this
			// error becomes the workflow status and its alert. Keep the cause only.
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return Conn{}, fmt.Errorf("address %s is not a valid Redis URL: %w", redactURL(raw), err)
		}
		c = Conn{Addr: opts.Addr, Username: opts.Username, Password: opts.Password, DB: opts.DB, TLS: opts.TLSConfig != nil}
	} else {
		c.Addr = withDefaultPort(raw)
	}
	if v := get("username"); v != "" {
		c.Username = v
	}
	if v := get("password"); v != "" {
		c.Password = v
	}
	if parseBool(get("tls")) {
		c.TLS = true
	}
	if db := strings.TrimSpace(get("db")); db != "" {
		n, err := strconv.Atoi(db)
		if err != nil || n < 0 {
			return Conn{}, fmt.Errorf("db %q is not a database number", db)
		}
		c.DB = n
	}
	return c, nil
}

// withDefaultPort adds Redis's default port to an address that names only a host.
func withDefaultPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	host := strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		// Neither host:port nor an IPv6 literal; leave it for the dial to report.
		return addr
	}
	return net.JoinHostPort(host, defaultPort)
}

// redactURL drops a URL's password so it can go into an error message.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		scheme, _, _ := strings.Cut(raw, "://")
		return scheme + "://…"
	}
	return u.Redacted()
}

// ConnError is a failure to reach or log in to Redis, with what to check.
type ConnError struct {
	Addr   string
	Err    error
	Checks []string
}

func (e *ConnError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cannot connect to Redis at %s: %v", e.Addr, e.Err)
	if len(e.Checks) > 0 {
		b.WriteString("\nWhat to check:")
		for _, c := range e.Checks {
			b.WriteString("\n- ")
			b.WriteString(c)
		}
	}
	return b.String()
}

func (e *ConnError) Unwrap() error { return e.Err }

// ExplainConnError turns a failure to reach or log in to Redis into one that
// says what to check, in Hermod's settings and in the Redis server's own
// configuration. Any other error, and nil, is returned as it is.
func ExplainConnError(conn Conn, err error) error {
	if err == nil {
		return nil
	}
	var ce *ConnError
	if errors.As(err, &ce) {
		return err
	}
	checks := connChecks(conn, err)
	if checks == nil {
		return err
	}
	return &ConnError{Addr: conn.Addr, Err: err, Checks: checks}
}

func connChecks(conn Conn, err error) []string {
	if checks := networkChecks(conn, err); checks != nil {
		return checks
	}
	return replyChecks(err)
}

// networkChecks covers failures to reach the server at all.
func networkChecks(conn Conn, err error) []string {
	host, port, splitErr := net.SplitHostPort(conn.Addr)
	if splitErr != nil {
		host, port = conn.Addr, defaultPort
	}
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		checks := []string{
			fmt.Sprintf("Redis is running, and answers from the machine Hermod runs on: redis-cli -h %s -p %s PING should print PONG.", host, port),
			fmt.Sprintf("The port is the one Redis listens on: the port setting in redis.conf (6379 unless changed). This address uses %s.", port),
			"Redis accepts connections from other machines: bind in redis.conf defaults to 127.0.0.1 -::1, which only lets in clients on the Redis machine itself. Add the address Hermod connects to (or 0.0.0.0 on a private network), keep a password set because protected-mode refuses outside clients without one, and restart Redis.",
			fmt.Sprintf("No firewall or cloud security group rejects port %s between Hermod and Redis.", port),
		}
		docker := "If Hermod runs in Docker or Kubernetes, localhost is the container itself, not your machine: use the Redis service or container name, or host.docker.internal to reach Redis on the host."
		if isLoopback(host) {
			return append([]string{docker}, checks...)
		}
		return append(checks, docker)
	case errors.As(err, &dnsErr):
		return []string{
			fmt.Sprintf("The host name %q is spelled right and resolves from the machine Hermod runs on.", host),
			"In Docker or Kubernetes, use the Redis service name on the network Hermod is attached to.",
		}
	case isTimeout(err):
		return []string{
			fmt.Sprintf("The host %s is right and reachable from the machine Hermod runs on.", host),
			fmt.Sprintf("No firewall, VPN or cloud security group silently drops port %s.", port),
			"Redis is not overloaded or paused (a long BGSAVE, a full disk, or a blocked replica).",
		}
	}
	return nil
}

// replyChecks covers the server's own refusals, and a connection it closed
// before answering, which is most often a TLS mismatch.
func replyChecks(err error) []string {
	upper := strings.ToUpper(err.Error())
	switch {
	case strings.HasPrefix(upper, "NOAUTH"):
		return []string{
			"Redis asks for a password (requirepass or an ACL user) and this sink sends none: enter it in Password.",
		}
	case strings.HasPrefix(upper, "WRONGPASS"), strings.Contains(upper, "INVALID PASSWORD"), strings.Contains(upper, "INVALID USERNAME-PASSWORD"):
		return []string{
			"The password matches requirepass in redis.conf, or the ACL user's password when Username is set.",
			"With Redis 6+ ACLs, the user exists and is enabled: ACL LIST on the server shows it with 'on'.",
			"Leave Username empty to log in as the default user, which uses requirepass.",
		}
	case strings.HasPrefix(upper, "DENIED"):
		return []string{
			"Redis is in protected-mode: it refuses clients from other machines while it has no password. Set requirepass (and enter it here), or bind Redis to the address Hermod connects to. Turning protected-mode off without a password leaves Redis open to anyone who can reach it.",
		}
	case strings.Contains(upper, "MAX NUMBER OF CLIENTS"):
		return []string{
			"Redis has reached maxclients (redis.conf, 10000 by default). Raise it, or find what holds the connections: CLIENT LIST on the server.",
		}
	case strings.HasPrefix(upper, "NOPERM"):
		return []string{
			"The ACL user may not run this command or touch this key. Grant it, e.g. ACL SETUSER <user> +ping +xadd +set +hset +expire ~<key or stream pattern>.",
		}
	case strings.Contains(upper, "TLS"), strings.Contains(upper, "X509"), strings.Contains(upper, "CERTIFICATE"),
		upper == "EOF", strings.HasSuffix(upper, ": EOF"), errors.Is(err, syscall.ECONNRESET):
		return []string{
			"TLS on this sink matches the server: managed Redis (AWS ElastiCache with in-transit encryption, Azure Cache, Upstash, Redis Cloud) usually needs TLS on, or a rediss:// address; a plain redis-server needs it off.",
			"The port is the TLS port when TLS is on (tls-port in redis.conf), and the plain port when it is off.",
			"The server's certificate is valid for the host name in the address.",
		}
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

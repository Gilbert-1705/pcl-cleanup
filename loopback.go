package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// The automatic storage run deletes through the server's own HTTP API at
// core_url. core_url must therefore name this machine: 127.0.0.1, ::1 or
// localhost, on any port. checkCoreURL refuses anything else when the settings
// are read, and coreClient refuses to connect to a non-loopback address even
// if a name resolved somewhere unexpected. Redirects are not followed, so the
// process never talks to a host the operator did not set (see egress in
// module.yaml).

var errNotLoopback = errors.New("core_url has to point at this machine (127.0.0.1, ::1 or localhost)")

// checkCoreURL validates core_url and returns it without a trailing slash.
func checkCoreURL(raw string) (string, error) {
	raw = strings.TrimRight(raw, "/")

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%w: core_url is not an address", errSettings)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: core_url has to start with http:// or https://", errSettings)
	}

	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: core_url may not carry a user, a query or a fragment", errSettings)
	}

	if !loopbackHost(u.Hostname()) {
		return "", fmt.Errorf("%w: %w", errSettings, errNotLoopback)
	}

	return raw, nil
}

// loopbackHost reports whether host is localhost or a loopback IP address.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// coreClient is the HTTP client for core_url. It only connects to loopback
// addresses and does not follow redirects: a 3xx answer is returned as it is
// and counts as a failure.
func coreClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}

			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				return errNotLoopback
			}

			return nil
		},
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext

	return &http.Client{
		Timeout:   2 * time.Minute,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Package outboundhttp provides the HTTP client used by configurable notification senders.
package outboundhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"time"
)

var transport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = ipv6FirstDialContext(net.DefaultResolver.LookupIPAddr, dialer.DialContext)
	return t
}()

// NewClient returns an HTTP client that prefers IPv6 for dual-stack endpoints
// while still trying IPv4 when IPv6 is unavailable.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: transport, Timeout: timeout}
}

func ipv6FirstDialContext(
	lookup func(context.Context, string) ([]net.IPAddr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return dial(ctx, network, address)
		}

		addresses, err := lookup(ctx, host)
		if err != nil || len(addresses) == 0 {
			return dial(ctx, network, address)
		}
		sort.SliceStable(addresses, func(i, j int) bool {
			return addresses[i].IP.To4() == nil && addresses[j].IP.To4() != nil
		})

		var errs []error
		for _, ipAddr := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

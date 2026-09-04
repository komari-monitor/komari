package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/komari-monitor/komari/internal/config"
	"gorm.io/gorm"
)

// marketDownloadMaxBytes limits the response size of a market download.
const marketDownloadMaxBytes = 100 << 20

const marketDownloadTimeout = 45 * time.Second

var blockedMarketIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // RFC 6598 shared address space.
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1.
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking.
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2.
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3.
	netip.MustParsePrefix("240.0.0.0/4"),     // Reserved, including broadcast.
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation.
	netip.MustParsePrefix("fec0::/10"),       // Deprecated site-local unicast.
}

// protectedMarketDownloadTransport resolves a hostname once per connection,
// checks every result, and dials the validated IP directly. Proxies are
// disabled so the destination checked here is the destination actually used.
var protectedMarketDownloadTransport = newSSRFProtectedTransport()

// IsSSRFProtectionEnabled reports whether SSRF protection is enabled for
// market downloads. An unreadable setting is treated as enabled so callers
// using this status helper fail closed.
func IsSSRFProtectionEnabled() bool {
	enabled, err := ssrfProtectionEnabled()
	return err != nil || enabled
}

// DownloadMarketURL performs a market download. When SSRF protection is
// enabled, URLs resolving to private or internal addresses are rejected
// (including on every redirect hop); when disabled, downloads proceed
// without address filtering.
func DownloadMarketURL(rawURL string, maxSize int64) ([]byte, error) {
	if err := validateMarketDownloadURL(rawURL); err != nil {
		return nil, err
	}
	protectionEnabled, err := ssrfProtectionEnabled()
	if err != nil {
		return nil, fmt.Errorf("unable to determine SSRF protection state: %w", err)
	}

	transport := http.DefaultTransport
	if protectionEnabled {
		transport = protectedMarketDownloadTransport
	}
	client := &http.Client{
		Timeout:   marketDownloadTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return validateMarketDownloadURL(req.URL.String())
		},
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("response exceeds the %d byte limit", maxSize)
	}
	if len(data) == 0 {
		return nil, errors.New("empty response")
	}
	return data, nil
}

func validateMarketDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("only HTTP and HTTPS URLs are allowed")
	}
	return nil
}

func newSSRFProtectedTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.Dial = nil
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	transport.DialContext = dialMarketAddress
	return transport
}

func dialMarketAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid market download address %q: %w", address, err)
	}

	ips, err := resolveMarketIPs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve market download host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedMarketIP(ip) {
			return nil, errors.New("requests to private or internal addresses are not allowed")
		}
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		if network == "tcp4" && ip.To4() == nil {
			continue
		}
		if network == "tcp6" && ip.To4() != nil {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		return nil, fmt.Errorf("no compatible address found for market download host %q", host)
	}
	return nil, fmt.Errorf("failed to connect to market download host %q: %w", host, lastErr)
}

func resolveMarketIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}

	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		if address.IP != nil {
			ips = append(ips, address.IP)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("host did not resolve to an IP address")
	}
	return ips, nil
}

func isBlockedMarketIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return true
	}

	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	for _, prefix := range blockedMarketIPPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func ssrfProtectionEnabled() (bool, error) {
	enabled, err := config.GetAs[bool](config.SSRFProtectionEnabledKey)
	if err == nil {
		return enabled, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err := config.Set(config.SSRFProtectionEnabledKey, false); err != nil {
		return false, err
	}
	return false, nil
}

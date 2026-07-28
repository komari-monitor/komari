package outboundhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestIPv6FirstDialContextPrefersIPv6AndFallsBackToIPv4(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("192.0.2.1")},
			{IP: net.ParseIP("2001:db8::1")},
		}, nil
	}
	var attempted []string
	dial := func(_ context.Context, _ string, address string) (net.Conn, error) {
		attempted = append(attempted, address)
		return nil, errors.New("unreachable")
	}

	_, err := ipv6FirstDialContext(lookup, dial)(context.Background(), "tcp", "notify.example:443")
	if err == nil {
		t.Fatal("dial succeeded unexpectedly")
	}
	want := []string{"[2001:db8::1]:443", "192.0.2.1:443"}
	if !reflect.DeepEqual(attempted, want) {
		t.Fatalf("dial order = %v, want %v", attempted, want)
	}
}

func TestNewClientConnectsToIPv6Loopback(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })

	response, err := NewClient(time.Second).Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("GET IPv6 loopback: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

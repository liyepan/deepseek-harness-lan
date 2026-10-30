package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startGateway runs a gateway in front of upstream and returns its base URL.
func startGateway(t *testing.T, upstream string, token string) string {
	t.Helper()
	gw, err := New(Config{
		Listen:      "127.0.0.1:0",
		Upstream:    strings.TrimPrefix(upstream, "http://"),
		Token:       token,
		InjectToken: token != "",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ln, err := gw.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = gw.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return "http://" + ln.Addr().String()
}

type seen struct {
	host   string
	origin string
	query  string
	uri    string
}

func TestRewritesHostAndOrigin(t *testing.T) {
	var got seen
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = seen{host: r.Host, origin: r.Header.Get("Origin"), query: r.URL.RawQuery, uri: r.URL.Path}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	base := startGateway(t, upstream.URL, "")
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")

	req, err := http.NewRequest(http.MethodPost, base+"/api/session.cold", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "192.168.1.5:8080"
	req.Header.Set("Origin", "http://192.168.1.5:8080")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	if got.host != upstreamHost {
		t.Errorf("upstream Host = %q, want %q", got.host, upstreamHost)
	}
	if want := "http://" + upstreamHost; got.origin != want {
		t.Errorf("upstream Origin = %q, want %q", got.origin, want)
	}
	if got.uri != "/api/session.cold" {
		t.Errorf("upstream path = %q", got.uri)
	}
}

func TestInjectsTokenOnlyForTrustedHost(t *testing.T) {
	var query string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	base := startGateway(t, upstream.URL, "secret-token")

	// A loopback Host is trusted: the navigation gets the token.
	req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
	req.Host = "127.0.0.1:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(query, "token=secret-token") {
		t.Errorf("token was not injected, query = %q", query)
	}

	// A foreign Host must never receive the token: that is the DNS rebinding path.
	query = ""
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.Host = "evil.example:8080"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Contains(query, "token=") {
		t.Errorf("token leaked to an untrusted Host: %q", query)
	}

	// An already authenticated browser is not re-injected.
	query = ""
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(&http.Cookie{Name: "dsh-auth-abc", Value: "v1.x.y"})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Contains(query, "token=") {
		t.Errorf("token was re-injected for an authenticated browser: %q", query)
	}

	// Only the root navigation is eligible.
	query = ""
	req, _ = http.NewRequest(http.MethodGet, base+"/assets/app.js", nil)
	req.Host = "127.0.0.1:8080"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Contains(query, "token=") {
		t.Errorf("token was injected into a subresource request: %q", query)
	}
}

func TestUpgradeIsProxied(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(brw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		brw.Flush()
		line, err := brw.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Fprintf(brw, "echo:%s", line)
		brw.Flush()
	}))
	defer upstream.Close()

	base := startGateway(t, upstream.URL, "")
	addr := strings.TrimPrefix(base, "http://")

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /api/remote.mux HTTP/1.1\r\nHost: 192.168.1.5:8080\r\nOrigin: http://192.168.1.5:8080\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("upgrade status = %q, want 101", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}

	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	echo, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got := strings.TrimSpace(echo); got != "echo:ping" {
		t.Fatalf("echo = %q", got)
	}
}

func TestUpstreamDownShowsReadablePage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close() // nothing listens there any more

	base := startGateway(t, upstreamURL, "")
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(string(body), "dsh web") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"192.168.1.5:8080": "192.168.1.5",
		"192.168.1.5":      "192.168.1.5",
		"LOCALHOST:9000":   "localhost",
		"[::1]:8080":       "[::1]",
		"[::1]":            "[::1]",
		"":                 "",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// Package gateway exposes a loopback-only dsh web server to the local network.
//
// # Why a proxy
//
// "dsh web" refuses to bind anything but loopback (--host 0.0.0.0 is rejected
// on purpose: the browser UI reaches a shell). The gateway therefore listens
// on the network and speaks to dsh over loopback.
//
// # Why header rewriting is required
//
// dsh fences every /api request and every WebSocket upgrade with a Host/Origin
// check: the Host authority must be loopback or explicitly trusted, and a
// present Origin must equal it. Since this tool adds no trust entries (the
// dsh command line stays exactly as the user gave it), the gateway rewrites
// both headers to the loopback authority dsh already knows. The browser keeps
// using the LAN origin; dsh only ever sees 127.0.0.1:3080.
//
// dsh's browser cookie is bound to that authority: its name is a hash of the
// Host value and its signed payload repeats it. Because both headers are
// rewritten on every request, the cookie dsh mints and later verifies is
// always the loopback one, while the browser stores it for whichever LAN
// address it actually used. That is what makes every LAN address work without
// configuring a single one of them.
package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/lan"
)

// Config is one gateway deployment.
type Config struct {
	// Listen is the local address to bind, e.g. "0.0.0.0:8080".
	Listen string
	// Upstream is the loopback authority of dsh, e.g. "127.0.0.1:3080".
	Upstream string
	// Token is the dsh launch token, when known. It is used only to
	// transparently log a browser in on its first navigation.
	Token string
	// AllowHosts are extra Host names (hostname or host:port) that may
	// receive the transparent token login. Addresses detected on this host
	// and loopback are always included.
	AllowHosts []string
	// InjectToken enables the transparent first-navigation login.
	InjectToken bool
	// Quiet disables the per-request log line.
	Quiet bool
	// Log receives request lines; nil discards them.
	Log io.Writer
}

// Gateway is the running network face of one dsh web instance.
type Gateway struct {
	cfg         Config
	proxy       *httputil.ReverseProxy
	upstreamURL *url.URL
	upstream    string
	origin      string
	allowed     map[string]bool
	logw        io.Writer
	logMu       sync.Mutex
}

// New validates the configuration and builds the reverse proxy.
func New(cfg Config) (*Gateway, error) {
	if cfg.Listen == "" {
		return nil, errors.New("gateway: empty listen address")
	}
	if cfg.Upstream == "" {
		return nil, errors.New("gateway: empty upstream address")
	}

	allowed := map[string]bool{
		"127.0.0.1": true,
		"localhost": true,
		"[::1]":     true,
		"::1":       true,
	}
	for _, a := range lan.All() {
		allowed[a.IP.String()] = true
	}
	for _, h := range cfg.AllowHosts {
		if h = normalizeHost(h); h != "" {
			allowed[h] = true
		}
	}

	g := &Gateway{
		cfg:         cfg,
		upstreamURL: &url.URL{Scheme: "http", Host: cfg.Upstream},
		upstream:    cfg.Upstream,
		origin:      "http://" + cfg.Upstream,
		allowed:     allowed,
		logw:        cfg.Log,
	}
	if g.logw == nil {
		g.logw = io.Discard
	}

	g.proxy = &httputil.ReverseProxy{
		Rewrite:       g.rewrite,
		Transport:     newTransport(),
		FlushInterval: -1,
		ErrorHandler:  g.upstreamError,
	}
	return g, nil
}

// Listen binds the configured address, so a caller can report the real port
// before it starts serving.
func (g *Gateway) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", g.cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w", g.cfg.Listen, err)
	}
	return ln, nil
}

// Serve blocks until ctx is cancelled or the listener fails.
func (g *Gateway) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           g.handler(),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.Serve(ln)
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Addr is the address the gateway was configured to bind.
func (g *Gateway) Addr() string { return g.cfg.Listen }

// handler serves every request, logging the outcome.
func (g *Gateway) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		g.proxy.ServeHTTP(rec, r)
		if !g.cfg.Quiet {
			g.logRequest(r, rec, time.Since(start))
		}
	})
}

// rewrite is the whole trick: dsh must see a loopback request, whatever the
// browser said.
func (g *Gateway) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(g.upstreamURL)
	// The Host header is what dsh's fence reads; it must be the loopback
	// authority. Assigning Out.Host wins over the URL host.
	pr.Out.Host = g.upstream

	// The fence also requires a present Origin to equal that authority.
	// Browsers send the LAN origin, so it is replaced as well.
	if pr.In.Header.Get("Origin") != "" {
		pr.Out.Header.Set("Origin", g.origin)
	}

	if g.shouldInjectToken(pr.In) {
		q := pr.Out.URL.Query()
		q.Set("token", g.cfg.Token)
		pr.Out.URL.RawQuery = q.Encode()
	}
}

// shouldInjectToken decides whether this navigation gets the launch token
// appended so a browser is logged in without a URL the user has to copy.
//
// The Host allowlist is a security boundary, not a convenience: rewriting a
// foreign Host to loopback would otherwise let a DNS-rebinding page reach this
// gateway and receive a freshly minted session cookie. Only addresses that
// belong to this machine may receive one.
func (g *Gateway) shouldInjectToken(r *http.Request) bool {
	if !g.cfg.InjectToken || g.cfg.Token == "" {
		return false
	}
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		return false
	}
	if r.URL.Query().Has("token") {
		return false
	}
	if !g.allowed[normalizeHost(r.Host)] {
		return false
	}
	// An authenticated browser needs no token. This is only an optimisation:
	// replaying the token is harmless, because dsh simply mints a fresh
	// cookie. The prefix is dsh's own; if it ever changes this check stops
	// matching and the injection becomes merely redundant.
	for _, c := range r.Cookies() {
		if strings.HasPrefix(c.Name, "dsh-auth-") {
			return false
		}
	}
	return true
}

// upstreamError answers with a readable page when dsh is not there.
func (g *Gateway) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, upstreamErrorPage, html.EscapeString(g.upstream), html.EscapeString(err.Error()))
}

func (g *Gateway) logRequest(r *http.Request, rec *recorder, d time.Duration) {
	upgrade := ""
	if r.Header.Get("Upgrade") != "" {
		upgrade = " (upgrade)"
	}
	client := r.RemoteAddr
	if h, _, err := net.SplitHostPort(client); err == nil {
		client = h
	}
	g.logMu.Lock()
	defer g.logMu.Unlock()
	fmt.Fprintf(g.logw, "%s  %-6s %-42s %3d %7s  %s -> %s%s\n",
		time.Now().Format("15:04:05"), r.Method, redact(r.URL), rec.status,
		d.Round(time.Millisecond), client, r.Host, upgrade)
}

// redact hides the launch token from logs.
func redact(u *url.URL) string {
	if !u.Query().Has("token") {
		return u.RequestURI()
	}
	q := u.Query()
	q.Set("token", "***")
	return u.Path + "?" + q.Encode()
}

// normalizeHost lowercases a Host header value and drops its port.
func normalizeHost(hostport string) string {
	h := strings.ToLower(strings.TrimSpace(hostport))
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "[") {
		if end := strings.Index(h, "]"); end != -1 {
			return h[:end+1]
		}
		return h
	}
	if i := strings.LastIndex(h, ":"); i != -1 && strings.Count(h, ":") == 1 {
		return h[:i]
	}
	return h
}

// newTransport tunes the loopback hop for streaming and upgrades.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil, // never route loopback traffic through an outbound proxy
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		// Pass Accept-Encoding through untouched. A transport that negotiated
		// gzip itself would decompress, breaking Content-Length and the
		// event-stream framing dsh relies on.
		DisableCompression: true,
	}
}

// recorder remembers the status while forwarding everything the reverse proxy
// needs for upgrades and streaming.
type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer, which is how
// the reverse proxy hijacks a connection for a WebSocket upgrade.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("gateway: response writer does not support hijacking")
	}
	r.wrote = true
	r.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

const upstreamErrorPage = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<title>dsh-lan: 上游未就绪</title>
<style>
  body{font:15px/1.7 system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;padding:48px 24px;background:#0f1115;color:#e6e6e6}
  main{max-width:640px;margin:0 auto}
  h1{font-size:20px;margin:0 0 16px}
  code{background:#1b1f27;border-radius:4px;padding:2px 6px}
  pre{background:#1b1f27;border-radius:6px;padding:12px;overflow:auto;font-size:13px}
  p{color:#a9b0bd}
</style></head><body><main>
<h1>dsh web 没有在 %s 上响应</h1>
<p>网关本身是正常的，但转发的目标没有应答。最常见的原因是 dsh 进程已经退出，或者启动失败。</p>
<pre>%s</pre>
<p>在终端里重新运行 <code>dsh-lan</code>，或先手动确认 <code>dsh web --host 127.0.0.1 --port 3080 --no-open</code> 能正常启动。</p>
</main></body></html>
`

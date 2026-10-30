package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/dsh"
	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/gateway"
	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/lan"
	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/state"
)

const (
	defaultListen   = "0.0.0.0:8080"
	defaultState    = ".dsh-lan/state.json"
	defaultUpstream = "127.0.0.1:3080"
	tokenWait       = 60 * time.Second
	shutdownGrace   = 8 * time.Second
)

type startOptions struct {
	listen     string
	upstream   string
	dshBin     string
	attach     bool
	token      string
	statePath  string
	allowHosts []string
	noInject   bool
	quiet      bool
}

func parseStart(args []string, errw io.Writer) (*startOptions, error) {
	opts := &startOptions{
		listen:    defaultListen,
		upstream:  defaultUpstream,
		statePath: defaultState,
	}
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.StringVar(&opts.listen, "listen", opts.listen, "address to bind (default 0.0.0.0:8080)")
	fs.StringVar(&opts.upstream, "upstream", opts.upstream, "dsh web address (default 127.0.0.1:3080)")
	fs.StringVar(&opts.dshBin, "dsh", "", "dsh executable (default: resolved from your login shell)")
	fs.BoolVar(&opts.attach, "attach", false, "never spawn dsh")
	fs.StringVar(&opts.token, "token", "", "dsh launch token")
	fs.StringVar(&opts.statePath, "state", opts.statePath, "token cache path")
	fs.BoolVar(&opts.noInject, "no-token-inject", false, "never append the launch token automatically")
	fs.BoolVar(&opts.quiet, "quiet", false, "suppress the per-request log line")
	fs.Func("allow-host", "extra Host name for the token login (repeatable)", func(v string) error {
		opts.allowHosts = append(opts.allowHosts, v)
		return nil
	})
	fs.Usage = func() { usage(errw) }
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if _, _, err := net.SplitHostPort(opts.upstream); err != nil {
		return nil, fmt.Errorf("--upstream must be host:port, got %q", opts.upstream)
	}
	return opts, nil
}

func runStart(args []string, out, errw io.Writer) int {
	opts, err := parseStart(args, errw)
	if err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	token := opts.token
	if token == "" {
		if st, loadErr := state.Load(opts.statePath); loadErr == nil {
			token = st.Token
		}
	}

	upstreamUp := dsh.UpstreamUp(opts.upstream, 500*time.Millisecond)
	var proc *dsh.Process

	switch {
	case opts.attach && !upstreamUp:
		fmt.Fprintf(errw, "dsh-lan: --attach was given but nothing answers on %s\n", opts.upstream)
		return 1
	case opts.attach:
		fmt.Fprintf(out, "dsh-lan: attaching to the dsh already serving %s\n", opts.upstream)
	case upstreamUp:
		fmt.Fprintf(out, "dsh-lan: %s is already serving; proxying it (pass --attach to say so explicitly)\n", opts.upstream)
	default:
		proc, token, err = spawnDsh(ctx, opts, out, errw)
		if err != nil {
			return 1
		}
	}

	if token != "" {
		_ = state.Save(opts.statePath, state.State{Token: token, Upstream: opts.upstream})
	}

	gw, err := gateway.New(gateway.Config{
		Listen:      opts.listen,
		Upstream:    opts.upstream,
		Token:       token,
		AllowHosts:  opts.allowHosts,
		InjectToken: !opts.noInject,
		Quiet:       opts.quiet,
		Log:         out,
	})
	if err != nil {
		fmt.Fprintf(errw, "dsh-lan: %v\n", err)
		stopProcess(proc)
		return 1
	}

	ln, err := gw.Listen()
	if err != nil {
		fmt.Fprintf(errw, "dsh-lan: %v\n", err)
		stopProcess(proc)
		return 1
	}

	banner(out, bannerData{
		listen:    displayListen(ln, opts.listen),
		upstream:  opts.upstream,
		port:      listenPort(ln, opts.listen),
		token:     token,
		addresses: lan.Addresses(),
		spawned:   proc,
	})

	serveErr := make(chan error, 1)
	go func() { serveErr <- gw.Serve(ctx, ln) }()

	if proc != nil {
		childDone := make(chan struct{})
		go func() {
			defer close(childDone)
			_ = proc.Wait()
		}()
		select {
		case err := <-serveErr:
			fmt.Fprintf(errw, "dsh-lan: gateway stopped: %v\n", err)
			stopProcess(proc)
			return 1
		case <-childDone:
			fmt.Fprintln(errw, "dsh-lan: dsh exited; shutting the gateway down")
			if tail := proc.Output(); tail != "" {
				fmt.Fprintf(errw, "-- last dsh output --\n%s\n", tail)
			}
			exitCode := 0
			if code, ok := dsh.IsExitError(proc.Wait()); ok {
				exitCode = code
			}
			return exitCode
		case <-ctx.Done():
			fmt.Fprintln(errw, "dsh-lan: shutting down")
			stopProcess(proc)
			<-serveErr
			return 0
		}
	}

	err = <-serveErr
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(errw, "dsh-lan: gateway stopped: %v\n", err)
		return 1
	}
	return 0
}

// spawnDsh starts the fixed dsh command line and waits for its launch token.
func spawnDsh(ctx context.Context, opts *startOptions, out, errw io.Writer) (*dsh.Process, string, error) {
	host, portStr, err := net.SplitHostPort(opts.upstream)
	if err != nil {
		return nil, "", err
	}
	if host != dsh.DefaultHost {
		fmt.Fprintf(errw, "dsh-lan: warning: --upstream host is %s, but this tool starts dsh on %s\n", host, dsh.DefaultHost)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, "", fmt.Errorf("--upstream port must be numeric, got %q", portStr)
	}

	resolved, err := dsh.Resolve(opts.dshBin)
	if err != nil {
		fmt.Fprintf(errw, "dsh-lan: %v\n", err)
		return nil, "", err
	}

	fmt.Fprintf(out, "dsh-lan: starting %s %s  (%s)\n",
		resolved.Bin, strings.Join(dsh.Args(host, port), " "), resolved.Source)
	proc, err := dsh.Start(resolved.Bin, host, port, resolved.Path, out, errw)
	if err != nil {
		fmt.Fprintf(errw, "dsh-lan: %v\n", err)
		return nil, "", err
	}

	select {
	case <-proc.Ready():
		return proc, proc.Token(), nil
	case <-proc.Done():
		_ = proc.Wait()
		fmt.Fprintf(errw, "dsh-lan: dsh exited before printing its URL\n-- last dsh output --\n%s\n", proc.Output())
		return nil, "", errors.New("dsh exited")
	case <-time.After(tokenWait):
		fmt.Fprintf(errw, "dsh-lan: dsh printed no URL within %s; continuing without a token\n", tokenWait)
		return proc, "", nil
	case <-ctx.Done():
		stopProcess(proc)
		return nil, "", ctx.Err()
	}
}

func stopProcess(proc *dsh.Process) {
	if proc != nil {
		_ = proc.Stop(shutdownGrace)
	}
}

// displayListen prefers the configured address: net.Listen reports the IPv6
// wildcard for an unspecified IPv4 host, which reads worse than "0.0.0.0:8080".
func displayListen(ln net.Listener, configured string) string {
	_, port, err := net.SplitHostPort(configured)
	if err == nil && port != "0" {
		return configured
	}
	return ln.Addr().String()
}

// listenPort reports the port actually bound, which matters when --listen used
// port 0.
func listenPort(ln net.Listener, configured string) int {
	if _, p, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		if n, convErr := strconv.Atoi(p); convErr == nil {
			return n
		}
	}
	_, p, _ := net.SplitHostPort(configured)
	n, _ := strconv.Atoi(p)
	return n
}

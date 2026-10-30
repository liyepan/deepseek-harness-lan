// Package cli implements the dsh-lan command line.
package cli

import (
	"fmt"
	"io"
	"strings"
)

// Version is reported by "dsh-lan version" and the startup banner. Override it
// at build time with: go build -ldflags "-X .../internal/cli.Version=1.2.3".
var Version = "0.1.0"

// Run dispatches one command line and returns the process exit code.
func Run(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		return runStart(nil, out, errw)
	}
	switch args[0] {
	case "start", "serve", "run":
		return runStart(args[1:], out, errw)
	case "token":
		return runToken(args[1:], out, errw)
	case "doctor":
		return runDoctor(args[1:], out, errw)
	case "version", "--version", "-v":
		fmt.Fprintf(out, "dsh-lan %s\n", Version)
		return 0
	case "help", "--help", "-h":
		usage(out)
		return 0
	}
	// A leading dash means "start with these flags".
	if strings.HasPrefix(args[0], "-") {
		return runStart(args, out, errw)
	}
	fmt.Fprintf(errw, "dsh-lan: unknown command %q\n\n", args[0])
	usage(errw)
	return 2
}

func usage(w io.Writer) {
	lines := []string{
		"dsh-lan " + Version + " - give a loopback-only \"dsh web\" a network face",
		"",
		"  dsh-lan [start] [flags]     start dsh if needed, then serve the LAN",
		"  dsh-lan doctor              check the environment and the ports",
		"  dsh-lan token [clear]       inspect or forget the cached launch token",
		"  dsh-lan version             print the version",
		"",
		"start flags:",
		"  --listen ADDR        address to bind (default 0.0.0.0:8080, all interfaces)",
		"  --upstream ADDR      dsh web address (default 127.0.0.1:3080)",
		"  --dsh PATH           dsh executable (default \"dsh\")",
		"  --attach             never spawn dsh, only proxy an already running one",
		"  --token TOKEN        dsh launch token (default: scraped from dsh, then cached)",
		"  --state PATH         token cache (default .dsh-lan/state.json)",
		"  --allow-host NAME    extra Host name allowed to receive the token login",
		"  --no-token-inject    never append the launch token automatically",
		"  --quiet              suppress the per-request log line",
		"",
		"The command this tool runs is always, unchanged:",
		"",
		"  dsh web --host 127.0.0.1 --port 3080 --no-open",
	}
	fmt.Fprintln(w, strings.Join(lines, "\n"))
}

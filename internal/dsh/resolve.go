package dsh

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Resolution is where dsh lives and which PATH can start it.
//
// dsh is a Node.js program whose launcher is "#!/usr/bin/env node", so the
// process that runs it needs a PATH that can also find node. Version managers
// put both in a directory that only a login shell knows about, which is why
// this is resolved the way a user's own terminal would: by asking the login
// shell. Nothing about that path is ever written into system configuration.
type Resolution struct {
	// Bin is the absolute path of the dsh executable.
	Bin string
	// Path is the PATH value that can start Bin, node included.
	Path string
	// Source says how it was found, for diagnostics.
	Source string
}

// Resolve finds dsh: an explicit value first, then the current PATH, then the
// user's login shell.
func Resolve(configured string) (Resolution, error) {
	if configured != "" {
		bin, err := exec.LookPath(configured)
		if err != nil {
			return Resolution{}, fmt.Errorf("cannot run --dsh %q: %w", configured, err)
		}
		if res, ok := viaShell(); ok && filepathDir(res.Bin) == filepathDir(bin) {
			res.Bin, res.Source = bin, "flag"
			return res, nil
		}
		return Resolution{Bin: bin, Path: os.Getenv("PATH"), Source: "flag"}, nil
	}

	if bin, err := exec.LookPath("dsh"); err == nil {
		if _, nodeErr := exec.LookPath("node"); nodeErr == nil {
			return Resolution{Bin: bin, Path: os.Getenv("PATH"), Source: "PATH"}, nil
		}
		// dsh is reachable but its interpreter is not: take the shell's PATH.
		if res, ok := viaShell(); ok {
			res.Source = "login shell (node was missing from PATH)"
			return res, nil
		}
		return Resolution{Bin: bin, Path: os.Getenv("PATH"), Source: "PATH"}, nil
	}

	if res, ok := viaShell(); ok {
		return res, nil
	}
	return Resolution{}, errors.New("dsh was not found; install it, or pass --dsh /path/to/dsh, or set DSH_LAN_DSH")
}

// viaShell asks the user's login shell where dsh and node are.
func viaShell() (Resolution, bool) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	// "###" cannot appear in a path, so the split is unambiguous.
	const script = "command -v dsh; echo '###'; command -v node >/dev/null && printf '%s' \"$PATH\""
	cmd := exec.Command(shell, "-lc", script)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return Resolution{}, false
	}
	head, tail, found := strings.Cut(string(out), "###")
	if !found {
		return Resolution{}, false
	}
	bin := strings.TrimSpace(head)
	path := strings.TrimSpace(tail)
	if bin == "" || path == "" {
		return Resolution{}, false
	}
	if info, statErr := os.Stat(bin); statErr != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return Resolution{}, false
	}
	return Resolution{Bin: bin, Path: path, Source: "login shell"}, true
}

// filepathDir is a tiny local helper so this file needs no path import.
func filepathDir(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

// envWithPath returns env with PATH replaced, never duplicated.
func envWithPath(env []string, path string) []string {
	if path == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
}

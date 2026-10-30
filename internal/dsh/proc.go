package dsh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultCommand is the exact command line this tool runs. Nothing is appended
// to it and nothing is rewritten: dsh-lan starts dsh, it does not configure it.
const DefaultCommand = "dsh web --host 127.0.0.1 --port 3080 --no-open"

const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 3080
)

// UpstreamUp reports whether something accepts TCP connections at addr.
func UpstreamUp(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Process is a supervised child started from one command line.
type Process struct {
	cmd     *exec.Cmd
	command string
	shell   string
	tail    *tailBuffer
	ready   chan struct{}

	mu        sync.RWMutex
	token     string
	readyOnce sync.Once

	done    chan struct{}
	waitErr error
	stopMu  sync.Mutex
	stopped bool
}

// Start runs command through the user's login shell.
//
// The login shell is the whole point: it is the environment the command was
// written for, with the same profile and the same PATH the user's terminal
// has. This tool therefore needs to know nothing about how dsh is installed or
// what it runs on. "exec" replaces the shell with the command, so signals and
// the exit status belong to the command itself rather than to a wrapper.
func Start(command string, stdout, stderr io.Writer) (*Process, error) {
	shell := shellPath()
	cmd := exec.Command(shell, "-lc", "exec "+command)
	cmd.Stdin = nil

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	p := &Process{
		cmd:     cmd,
		command: command,
		shell:   shell,
		tail:    newTailBuffer(120),
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %q: %w", command, err)
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		p.pump(stdoutPipe, stdout, true)
	}()
	go func() {
		defer readers.Done()
		p.pump(stderrPipe, stderr, false)
	}()
	go func() {
		readers.Wait()
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// shellPath picks the shell that represents the user's environment.
func shellPath() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if info, err := os.Stat(sh); err == nil && !info.IsDir() {
			return sh
		}
	}
	if info, err := os.Stat("/bin/bash"); err == nil && !info.IsDir() {
		return "/bin/bash"
	}
	return "/bin/sh"
}

// pump copies one child output stream while watching for the launch token.
func (p *Process) pump(r io.Reader, w io.Writer, parse bool) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		p.tail.add(line)
		if !parse {
			if w != nil {
				fmt.Fprintln(w, line)
			}
			continue
		}
		// dsh prints its launch token exactly once, on stdout. This tool
		// captures it; the terminal keeps only the address.
		if redacted, changed := RedactLaunchLine(line); changed {
			if w != nil {
				fmt.Fprintln(w, redacted)
			}
		} else if w != nil {
			fmt.Fprintln(w, line)
		}
		if token := TokenFromLine(line); token != "" {
			p.mu.Lock()
			p.token = token
			p.mu.Unlock()
			p.readyOnce.Do(func() { close(p.ready) })
		}
	}
}

// Ready is closed once dsh printed its token.
func (p *Process) Ready() <-chan struct{} { return p.ready }

// Done is closed when the child exits.
func (p *Process) Done() <-chan struct{} { return p.done }

// Token returns the launch token dsh printed, if any.
func (p *Process) Token() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.token
}

// Wait blocks until the child exits and returns its error, if it failed.
func (p *Process) Wait() error {
	<-p.done
	return p.waitErr
}

// Alive reports whether the child is still running.
func (p *Process) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// CommandLine is the command the user asked for, for diagnostics.
func (p *Process) CommandLine() string { return p.command }

// Shell is the login shell used to run it.
func (p *Process) Shell() string { return p.shell }

// Output returns the tail of the child output, for error reporting.
func (p *Process) Output() string { return p.tail.String() }

// Stop terminates the child: SIGTERM first, SIGKILL after grace. It is safe to
// call more than once.
func (p *Process) Stop(grace time.Duration) error {
	p.stopMu.Lock()
	if p.stopped {
		p.stopMu.Unlock()
		return nil
	}
	p.stopped = true
	p.stopMu.Unlock()

	if p.cmd.Process == nil {
		return nil
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-p.done:
		return nil
	case <-time.After(grace):
	}

	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// CommandWord is the program a command line names, resolved through the same
// login shell the command will run in. It is used by doctor only; nothing in
// the normal path needs to know where the program lives.
func CommandWord(command string) (string, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", errors.New("empty command")
	}
	word := fields[0]
	out, err := exec.Command(shellPath(), "-lc", "command -v "+word).Output()
	if err != nil {
		return "", fmt.Errorf("%q was not found in your login shell", word)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsExitError reports whether err is a non-zero child exit, returning its code.
func IsExitError(err error) (int, bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	return 0, false
}

// tailBuffer keeps the last n lines of output for diagnostics.
type tailBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func newTailBuffer(n int) *tailBuffer { return &tailBuffer{max: n} }

func (t *tailBuffer) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

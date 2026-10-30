package dsh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Defaults of the dsh web invocation this tool manages. The exact command line
// is part of the contract:
//
//	dsh web --host 127.0.0.1 --port 3080 --no-open
const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 3080
)

// Args is the fixed argv this tool hands to dsh. No extra flags are appended:
// the deployment stays exactly the command the user asked for.
func Args(host string, port int) []string {
	return []string{"web", "--host", host, "--port", strconv.Itoa(port), "--no-open"}
}

// UpstreamUp reports whether something accepts TCP connections at addr.
func UpstreamUp(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Process is a supervised "dsh web" child process.
type Process struct {
	cmd   *exec.Cmd
	bin   string
	args  []string
	tail  *tailBuffer
	ready chan struct{}

	mu        sync.RWMutex
	token     string
	readyOnce sync.Once

	done    chan struct{}
	waitErr error
	stopMu  sync.Mutex
	stopped bool
}

// Start launches dsh and streams its output to stdout/stderr verbatim. The
// returned process is live but may not have printed its token yet: wait on
// Ready.
func Start(bin string, host string, port int, stdout, stderr io.Writer) (*Process, error) {
	args := Args(host, port)
	cmd := exec.Command(bin, args...)
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
		cmd:   cmd,
		bin:   bin,
		args:  args,
		tail:  newTailBuffer(120),
		ready: make(chan struct{}),
		done:  make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %s: %w", bin, err)
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

// pump copies one child output stream while watching for the launch token.
func (p *Process) pump(r io.Reader, w io.Writer, parse bool) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		p.tail.add(line)
		if w != nil {
			fmt.Fprintln(w, line)
		}
		if !parse {
			continue
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

// CommandLine is the exact argv, for diagnostics.
func (p *Process) CommandLine() string {
	return strings.Join(append([]string{p.bin}, p.args...), " ")
}

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

// LookPath resolves the dsh executable.
func LookPath(bin string) (string, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("%q was not found in PATH; install dsh first or pass --dsh <path>", bin)
	}
	return path, nil
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

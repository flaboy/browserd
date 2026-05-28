package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var ErrLiveRuntimeUnhealthy = errors.New("live runtime unhealthy")

type LiveRuntimePlan struct {
	Display        string
	VNCPort        int
	WebsockifyPort int
	NoVNCWebRoot   string
}

type LiveCommand struct {
	Name string
	Args []string
	Env  []string
}

type LiveRuntime struct {
	Plan      LiveRuntimePlan
	processes []*liveProcess
	mu        sync.Mutex
}

type liveProcess struct {
	name   string
	cmd    *exec.Cmd
	output *tailBuffer
	done   chan error
}

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func NewLiveRuntimePlan(sessionRoot string) (LiveRuntimePlan, error) {
	vncPort, err := freeTCPPort()
	if err != nil {
		return LiveRuntimePlan{}, err
	}
	websockifyPort, err := freeTCPPort()
	if err != nil {
		return LiveRuntimePlan{}, err
	}
	displayNumber := (vncPort % 1000) + 100
	_ = sessionRoot
	return LiveRuntimePlan{
		Display:        ":" + strconv.Itoa(displayNumber),
		VNCPort:        vncPort,
		WebsockifyPort: websockifyPort,
		NoVNCWebRoot:   "/usr/share/novnc",
	}, nil
}

func NewLiveRuntime(sessionRoot string) (*LiveRuntime, error) {
	plan, err := NewLiveRuntimePlan(sessionRoot)
	if err != nil {
		return nil, err
	}
	return &LiveRuntime{Plan: plan}, nil
}

func (p LiveRuntimePlan) Commands() []LiveCommand {
	return []LiveCommand{
		{
			Name: "Xvfb",
			Args: []string{p.Display, "-screen", "0", "1440x1000x24", "-nolisten", "tcp"},
		},
		{
			Name: "openbox",
			Env:  []string{"DISPLAY=" + p.Display},
		},
		{
			Name: "x11vnc",
			Args: []string{"-display", p.Display, "-localhost", "-forever", "-shared", "-rfbport", strconv.Itoa(p.VNCPort), "-nopw"},
		},
		{
			Name: "websockify",
			Args: []string{"--web", p.NoVNCWebRoot, strconv.Itoa(p.WebsockifyPort), "127.0.0.1:" + strconv.Itoa(p.VNCPort)},
		},
	}
}

func (r *LiveRuntime) Start(ctx context.Context) error {
	for _, spec := range r.Plan.Commands() {
		proc, err := r.startProcess(ctx, spec)
		if err != nil {
			_ = r.Stop(context.Background())
			return err
		}
		r.mu.Lock()
		r.processes = append(r.processes, proc)
		r.mu.Unlock()
		if err := r.waitForCommandReady(ctx, spec.Name); err != nil {
			_ = r.Stop(context.Background())
			return err
		}
	}
	return nil
}

func (r *LiveRuntime) Stop(_ context.Context) error {
	r.mu.Lock()
	processes := append([]*liveProcess(nil), r.processes...)
	r.processes = nil
	r.mu.Unlock()

	for i := len(processes) - 1; i >= 0; i-- {
		proc := processes[i]
		if proc == nil {
			continue
		}
		cmd := proc.cmd
		if cmd == nil || cmd.Process == nil {
			continue
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-proc.done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-proc.done
		}
	}
	return nil
}

func (r *LiveRuntime) ProxyTarget() string {
	return "http://127.0.0.1:" + strconv.Itoa(r.Plan.WebsockifyPort)
}

func (r *LiveRuntime) Health(ctx context.Context) error {
	if err := r.processesHealthy(); err != nil {
		return err
	}
	if err := waitForTCPReady(ctx, r.Plan.VNCPort, 2*time.Second); err != nil {
		return fmt.Errorf("%w: x11vnc port %d is not ready: %v", ErrLiveRuntimeUnhealthy, r.Plan.VNCPort, err)
	}
	if err := waitForTCPReady(ctx, r.Plan.WebsockifyPort, 2*time.Second); err != nil {
		return fmt.Errorf("%w: websockify port %d is not ready: %v", ErrLiveRuntimeUnhealthy, r.Plan.WebsockifyPort, err)
	}
	return nil
}

func (r *LiveRuntime) ChromeEnv() []string {
	return []string{"DISPLAY=" + r.Plan.Display}
}

func (r *LiveRuntime) startProcess(ctx context.Context, spec LiveCommand) (*liveProcess, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Env = append(cmd.Environ(), spec.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output := &tailBuffer{limit: 32 * 1024}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	proc := &liveProcess{
		name:   spec.Name,
		cmd:    cmd,
		output: output,
		done:   make(chan error, 1),
	}
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()
	return proc, nil
}

func (r *LiveRuntime) waitForCommandReady(ctx context.Context, name string) error {
	switch name {
	case "Xvfb", "openbox":
		return r.waitForProcessStable(ctx, name, 100*time.Millisecond)
	case "x11vnc":
		if err := waitForTCPReady(ctx, r.Plan.VNCPort, 5*time.Second); err != nil {
			return fmt.Errorf("%w: x11vnc did not become ready on port %d: %v%s", ErrLiveRuntimeUnhealthy, r.Plan.VNCPort, err, r.processDiagnostics())
		}
	case "websockify":
		if err := waitForTCPReady(ctx, r.Plan.WebsockifyPort, 5*time.Second); err != nil {
			return fmt.Errorf("%w: websockify did not become ready on port %d: %v%s", ErrLiveRuntimeUnhealthy, r.Plan.WebsockifyPort, err, r.processDiagnostics())
		}
	}
	if err := r.processesHealthy(); err != nil {
		return err
	}
	return nil
}

func (r *LiveRuntime) waitForProcessStable(ctx context.Context, name string, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return r.processesHealthy()
	}
}

func (r *LiveRuntime) processesHealthy() error {
	r.mu.Lock()
	processes := append([]*liveProcess(nil), r.processes...)
	r.mu.Unlock()
	for _, proc := range processes {
		if proc == nil {
			continue
		}
		select {
		case err := <-proc.done:
			return fmt.Errorf("%w: %s exited: %v%s", ErrLiveRuntimeUnhealthy, proc.name, err, proc.diagnostics())
		default:
		}
	}
	return nil
}

func (r *LiveRuntime) processDiagnostics() string {
	r.mu.Lock()
	processes := append([]*liveProcess(nil), r.processes...)
	r.mu.Unlock()
	var parts []string
	for _, proc := range processes {
		if proc == nil {
			continue
		}
		if diag := proc.diagnostics(); diag != "" {
			parts = append(parts, proc.name+diag)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, "; ")
}

func (p *liveProcess) diagnostics() string {
	if p == nil || p.output == nil {
		return ""
	}
	out := strings.TrimSpace(p.output.String())
	if out == "" {
		return ""
	}
	return " output=" + strconv.Quote(out)
}

func waitForTCPReady(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return lastErr
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		b.limit = 32 * 1024
	}
	_, _ = b.buf.Write(p)
	if b.buf.Len() > b.limit {
		trim := b.buf.Len() - b.limit
		next := append([]byte(nil), b.buf.Bytes()[trim:]...)
		b.buf.Reset()
		_, _ = b.buf.Write(next)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func sessionRootFromProfileDir(profileDir string) string {
	return filepath.Dir(profileDir)
}

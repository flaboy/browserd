package browser

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

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
	processes []*exec.Cmd
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
		cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
		cmd.Env = append(cmd.Environ(), spec.Env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			_ = r.Stop(context.Background())
			return fmt.Errorf("start %s: %w", spec.Name, err)
		}
		r.processes = append(r.processes, cmd)
	}
	time.Sleep(250 * time.Millisecond)
	return nil
}

func (r *LiveRuntime) Stop(_ context.Context) error {
	for i := len(r.processes) - 1; i >= 0; i-- {
		cmd := r.processes[i]
		if cmd == nil || cmd.Process == nil {
			continue
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		done := make(chan struct{})
		go func(c *exec.Cmd) {
			_, _ = c.Process.Wait()
			close(done)
		}(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}
	r.processes = nil
	return nil
}

func (r *LiveRuntime) ProxyTarget() string {
	return "http://127.0.0.1:" + strconv.Itoa(r.Plan.WebsockifyPort)
}

func (r *LiveRuntime) ChromeEnv() []string {
	return []string{"DISPLAY=" + r.Plan.Display}
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

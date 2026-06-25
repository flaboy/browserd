package browser

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestLiveRuntimeCommandsUseConfiguredScreenSize(t *testing.T) {
	plan, err := NewLiveRuntimePlan("/tmp/session", 1920, 1080)
	if err != nil {
		t.Fatalf("new live runtime plan: %v", err)
	}
	commands := plan.Commands()
	if got := commands[0].Args[3]; got != "1920x1080x24" {
		t.Fatalf("expected Xvfb to use configured screen size, got %q in %+v", got, commands[0])
	}
}

func TestLiveRuntimePlanRejectsMissingScreenSize(t *testing.T) {
	_, err := NewLiveRuntimePlan("/tmp/session", 0, 1080)
	if err == nil {
		t.Fatal("expected missing screen size to fail")
	}
	_, err = NewLiveRuntimePlan("/tmp/session", 1920, 0)
	if err == nil {
		t.Fatal("expected missing screen size to fail")
	}
}

func TestLiveRuntimePlanAllocatesDisplayAndPorts(t *testing.T) {
	plan, err := NewLiveRuntimePlan("/tmp/session", 1920, 1080)
	if err != nil {
		t.Fatalf("new live runtime plan: %v", err)
	}
	if plan.Display == "" {
		t.Fatal("expected display")
	}
	if plan.VNCPort <= 0 {
		t.Fatalf("expected vnc port, got %d", plan.VNCPort)
	}
	if plan.WebsockifyPort <= 0 {
		t.Fatalf("expected websockify port, got %d", plan.WebsockifyPort)
	}
	if plan.VNCPort == plan.WebsockifyPort {
		t.Fatalf("ports must be unique: %+v", plan)
	}
	if plan.ScreenWidth != 1920 || plan.ScreenHeight != 1080 {
		t.Fatalf("expected configured screen size, got %+v", plan)
	}
}

func TestLiveRuntimeCommandsUseSessionDisplay(t *testing.T) {
	plan := LiveRuntimePlan{
		Display:        ":99",
		VNCPort:        5901,
		WebsockifyPort: 6081,
		NoVNCWebRoot:   "/usr/share/novnc",
		ScreenWidth:    1920,
		ScreenHeight:   1080,
	}
	commands := plan.Commands()
	if len(commands) != 4 {
		t.Fatalf("expected four commands, got %+v", commands)
	}
	if commands[0].Name != "Xvfb" {
		t.Fatalf("expected Xvfb first, got %+v", commands)
	}
	if commands[2].Name != "x11vnc" {
		t.Fatalf("expected x11vnc third, got %+v", commands)
	}
	if commands[3].Name != "websockify" {
		t.Fatalf("expected websockify fourth, got %+v", commands)
	}
	if commands[2].Args[1] != ":99" {
		t.Fatalf("expected x11vnc to use display, got %+v", commands[2])
	}
}

func TestLiveRuntimeHealthFailsWhenVNCIsNotListening(t *testing.T) {
	vncPort := freePortForTest(t)
	websockifyPort := freePortForTest(t)
	runtime := &LiveRuntime{
		Plan: LiveRuntimePlan{
			Display:        ":99",
			VNCPort:        vncPort,
			WebsockifyPort: websockifyPort,
			NoVNCWebRoot:   "/usr/share/novnc",
			ScreenWidth:    1920,
			ScreenHeight:   1080,
		},
	}

	err := runtime.Health(context.Background())
	if !errors.Is(err, ErrLiveRuntimeUnhealthy) {
		t.Fatalf("expected ErrLiveRuntimeUnhealthy, got %v", err)
	}
}

func TestLiveRuntimeHealthFailsWhenWebsockifyIsNotListening(t *testing.T) {
	vncListener, vncPort := listenLocalForTest(t)
	defer vncListener.Close()
	go func() {
		conn, err := vncListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("RFB 003.008\n"))
	}()

	runtime := &LiveRuntime{
		Plan: LiveRuntimePlan{
			Display:        ":99",
			VNCPort:        vncPort,
			WebsockifyPort: freePortForTest(t),
			NoVNCWebRoot:   "/usr/share/novnc",
			ScreenWidth:    1920,
			ScreenHeight:   1080,
		},
	}

	err := runtime.Health(context.Background())
	if !errors.Is(err, ErrLiveRuntimeUnhealthy) {
		t.Fatalf("expected ErrLiveRuntimeUnhealthy, got %v", err)
	}
}

func listenLocalForTest(t *testing.T) (net.Listener, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen local: %v", err)
	}
	return listener, listener.Addr().(*net.TCPAddr).Port
}

func freePortForTest(t *testing.T) int {
	t.Helper()
	listener, port := listenLocalForTest(t)
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	return port
}

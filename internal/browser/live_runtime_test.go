package browser

import "testing"

func TestLiveRuntimePlanAllocatesDisplayAndPorts(t *testing.T) {
	plan, err := NewLiveRuntimePlan("/tmp/session")
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
}

func TestLiveRuntimeCommandsUseSessionDisplay(t *testing.T) {
	plan := LiveRuntimePlan{
		Display:        ":99",
		VNCPort:        5901,
		WebsockifyPort: 6081,
		NoVNCWebRoot:   "/usr/share/novnc",
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

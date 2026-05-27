package router

import "testing"

func TestExtractLiveViewToken_UsesFirstPathSegment(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "root", path: "/v/tok_1/", want: "tok_1"},
		{name: "asset path", path: "/v/tok_1/vnc.html", want: "tok_1"},
		{name: "websocket path", path: "/v/tok_1/websockify", want: "tok_1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLiveViewToken(tt.path, "/v"); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

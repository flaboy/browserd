package profile

import "testing"

func TestParseS3ProfilePath(t *testing.T) {
	k, err := parseS3ProfilePath("/browser-sessions/t/c/s/profile.tgz")
	if err != nil {
		t.Fatalf("parse s3 profile path: %v", err)
	}
	if k != "browser-sessions/t/c/s/profile.tgz" {
		t.Fatalf("key mismatch: %s", k)
	}
}

func TestParseS3ProfilePath_RejectsInvalid(t *testing.T) {
	if _, err := parseS3ProfilePath("http://x/y"); err == nil {
		t.Fatalf("expected invalid scheme error")
	}
	if _, err := parseS3ProfilePath("s3://private/browser-sessions/t/c/s/profile.tgz"); err == nil {
		t.Fatalf("expected invalid scheme error")
	}
	if _, err := parseS3ProfilePath("browser-sessions/t/c/s/profile.tgz"); err == nil {
		t.Fatalf("expected missing leading slash error")
	}
}

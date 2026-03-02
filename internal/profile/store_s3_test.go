package profile

import "testing"

func TestParseS3URI(t *testing.T) {
	b, k, err := parseS3URI("s3://private/browser-sessions/t/c/s/profile.tgz")
	if err != nil {
		t.Fatalf("parse s3 uri: %v", err)
	}
	if b != "private" {
		t.Fatalf("bucket mismatch: %s", b)
	}
	if k != "browser-sessions/t/c/s/profile.tgz" {
		t.Fatalf("key mismatch: %s", k)
	}
}

func TestParseS3URI_RejectsInvalid(t *testing.T) {
	if _, _, err := parseS3URI("http://x/y"); err == nil {
		t.Fatalf("expected invalid scheme error")
	}
	if _, _, err := parseS3URI("s3://"); err == nil {
		t.Fatalf("expected invalid uri error")
	}
}

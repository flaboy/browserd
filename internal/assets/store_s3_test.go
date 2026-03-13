package assets

import "testing"

func TestParseS3URI(t *testing.T) {
	bucket, key, err := parseS3URI("s3://browserd-snapshots/team_1/conv_1/1737373333.png")
	if err != nil {
		t.Fatalf("parse s3 uri: %v", err)
	}
	if bucket != "browserd-snapshots" {
		t.Fatalf("bucket mismatch: %s", bucket)
	}
	if key != "team_1/conv_1/1737373333.png" {
		t.Fatalf("key mismatch: %s", key)
	}
}

func TestParseS3URI_RejectsInvalid(t *testing.T) {
	if _, _, err := parseS3URI("http://example.com/x.png"); err == nil {
		t.Fatalf("expected invalid scheme error")
	}
	if _, _, err := parseS3URI("s3://"); err == nil {
		t.Fatalf("expected invalid uri error")
	}
}

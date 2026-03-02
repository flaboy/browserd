package profile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestPackAndUnpackTGZ_RoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tgz := filepath.Join(t.TempDir(), "profile.tgz")
	if err := PackDirToTGZ(src, tgz); err != nil {
		t.Fatalf("pack: %v", err)
	}

	dst := t.TempDir()
	if err := UnpackTGZToDir(tgz, dst); err != nil {
		t.Fatalf("unpack: %v", err)
	}

	a, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	if string(a) != "hello" {
		t.Fatalf("unexpected a.txt: %q", string(a))
	}
	b, err := os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if string(b) != "world" {
		t.Fatalf("unexpected b.txt: %q", string(b))
	}
}

func TestUnpackTGZToDir_RejectsPathTraversal(t *testing.T) {
	buf := new(bytes.Buffer)
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "../../evil.txt",
		Typeflag: tar.TypeReg,
		Size:     int64(len("x")),
		Mode:     0o644,
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	src := filepath.Join(t.TempDir(), "bad.tgz")
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	err := UnpackTGZToDir(src, t.TempDir())
	if err == nil {
		t.Fatalf("expected error for traversal archive")
	}
}

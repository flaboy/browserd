package profile

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func PackDirToTGZ(srcDir, dstTGZ string) error {
	srcDir = filepath.Clean(srcDir)
	if fi, err := os.Stat(srcDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("invalid srcDir: %s", srcDir)
	}
	if err := os.MkdirAll(filepath.Dir(dstTGZ), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dstTGZ)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		_, err = io.Copy(tw, file)
		return err
	})
}

func UnpackTGZToDir(srcTGZ, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	f, err := os.Open(srcTGZ)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	cleanBase := filepath.Clean(dstDir) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, filepath.FromSlash(hdr.Name))
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget+string(os.PathSeparator), cleanBase) &&
			cleanTarget != strings.TrimSuffix(cleanBase, string(os.PathSeparator)) {
			return fmt.Errorf("invalid archive path: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
				return err
			}
			out, err := os.Create(cleanTarget)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// ignore unsupported entries in v1
		}
	}
	return nil
}

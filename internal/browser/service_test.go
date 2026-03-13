package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	browserrt "browserd/internal/runtime"
)

type fakeAssetStore struct {
	puts []fakeAssetPut
	err  error
}

type fakeAssetPut struct {
	URI         string
	Body        []byte
	ContentType string
}

func (f *fakeAssetStore) Put(_ context.Context, uri string, body []byte, contentType string) error {
	f.puts = append(f.puts, fakeAssetPut{URI: uri, Body: append([]byte(nil), body...), ContentType: contentType})
	return f.err
}

func TestBuildChromeArgs_IncludesNoSandboxAndProfileDir(t *testing.T) {
	args := buildChromeArgs("/tmp/profile")

	hasNoSandbox := false
	hasUserDataDir := false
	for _, arg := range args {
		if arg == "--no-sandbox" {
			hasNoSandbox = true
		}
		if arg == "--user-data-dir=/tmp/profile" {
			hasUserDataDir = true
		}
	}

	if !hasNoSandbox {
		t.Fatalf("expected --no-sandbox in chrome args")
	}
	if !hasUserDataDir {
		t.Fatalf("expected user-data-dir arg")
	}
}

func TestWaitForDevToolsWS_ReturnsWebSocketURLFromActivePortFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte("12345\n/devtools/browser/abc\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	got, err := waitForDevToolsWS(dir, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected ready websocket, got %v", err)
	}
	if got != "ws://127.0.0.1:12345/devtools/browser/abc" {
		t.Fatalf("unexpected ws url: %s", got)
	}
}

func TestBuildChromeArgs_KeepsAboutBlankBootstrapPage(t *testing.T) {
	args := buildChromeArgs("/tmp/profile")
	if args[len(args)-1] != "about:blank" {
		t.Fatalf("expected about:blank bootstrap page, got %+v", args)
	}
}

func TestSnapshotOutput_UsesPageAsSingleStructure(t *testing.T) {
	out := SnapshotOutput{
		SnapshotID: "snap_1",
		Page: PageSnapshot{
			URL:   "https://example.com",
			Title: "Example",
			Groups: map[string]PageTable{
				"buttons": {
					Columns: []string{"ref", "tag", "text"},
					Rows:    [][]any{{"e1", "BUTTON", "Submit"}},
				},
			},
		},
	}

	if out.Page.URL == "" || out.Page.Title == "" {
		t.Fatalf("page metadata missing")
	}
	if _, ok := out.Page.Groups["buttons"]; !ok {
		t.Fatalf("expected buttons group")
	}
}

func TestValidateActionRef_RejectsTextRefForClick(t *testing.T) {
	err := validateActionRef("click", browserrt.RefState{
		Ref:  "t1",
		Kind: "text",
	})
	if err == nil {
		t.Fatalf("expected invalid ref for click on text ref")
	}
	if err != browserrt.ErrInvalidRef {
		t.Fatalf("expected ErrInvalidRef, got %v", err)
	}
}

func TestValidateActionRef_AllowsTextRefForScrollIntoView(t *testing.T) {
	err := validateActionRef("scrollIntoView", browserrt.RefState{
		Ref:  "t1",
		Kind: "text",
	})
	if err != nil {
		t.Fatalf("expected scrollIntoView to allow text refs, got %v", err)
	}
}

func TestUploadAfterNavigate_UploadsScreenshotWhenS3PathProvided(t *testing.T) {
	store := &fakeAssetStore{}
	svc := &Service{
		assets: store,
		capturePNG: func(context.Context) ([]byte, error) {
			return []byte("png-bytes"), nil
		},
	}

	err := svc.uploadAfterNavigate(context.Background(), "s3://browserd-snapshots/team_1/conv_1/1737373333.png")
	if err != nil {
		t.Fatalf("uploadAfterNavigate returned error: %v", err)
	}
	if len(store.puts) != 1 {
		t.Fatalf("expected one put, got %+v", store.puts)
	}
	if store.puts[0].URI != "s3://browserd-snapshots/team_1/conv_1/1737373333.png" {
		t.Fatalf("unexpected upload uri: %+v", store.puts[0])
	}
	if store.puts[0].ContentType != "image/png" {
		t.Fatalf("unexpected content type: %+v", store.puts[0])
	}
	if string(store.puts[0].Body) != "png-bytes" {
		t.Fatalf("unexpected body: %+v", store.puts[0])
	}
}

func TestUploadAfterNavigate_UsesRequestedBucketInsteadOfProfileBucket(t *testing.T) {
	store := &fakeAssetStore{}
	svc := &Service{
		assets: store,
		capturePNG: func(context.Context) ([]byte, error) {
			return []byte("png-bytes"), nil
		},
	}

	err := svc.uploadAfterNavigate(context.Background(), "s3://separate-snapshot-bucket/team_1/conv_1/1737373333.png")
	if err != nil {
		t.Fatalf("uploadAfterNavigate returned error: %v", err)
	}
	if len(store.puts) != 1 || store.puts[0].URI != "s3://separate-snapshot-bucket/team_1/conv_1/1737373333.png" {
		t.Fatalf("expected requested snapshot bucket to be used, got %+v", store.puts)
	}
}

func TestUploadAfterNavigate_ReturnsCaptureError(t *testing.T) {
	svc := &Service{
		assets: &fakeAssetStore{},
		capturePNG: func(context.Context) ([]byte, error) {
			return nil, errors.New("capture failed")
		},
	}

	err := svc.uploadAfterNavigate(context.Background(), "s3://browserd-snapshots/team_1/conv_1/1737373333.png")
	if err == nil || err.Error() != "capture failed" {
		t.Fatalf("expected capture failure, got %v", err)
	}
}

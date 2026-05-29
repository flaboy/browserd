package liveviewer

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/index.html dist/assets/*
var files embed.FS

func IndexHTML() ([]byte, error) {
	return files.ReadFile("dist/index.html")
}

func StaticHandler() http.Handler {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		})
	}
	return http.FileServer(http.FS(sub))
}

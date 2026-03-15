package ui

import (
	"embed"
	"io/fs"
)

//go:embed templates/* static/*
var Files embed.FS

func StaticFiles() fs.FS {
	staticFS, err := fs.Sub(Files, "static")
	if err != nil {
		panic(err)
	}

	return staticFS
}

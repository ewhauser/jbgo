package main

import (
	"embed"
)

//go:embed manifest.json
var assetFS embed.FS

func loadManifest() (*manifest, error) {
	data, err := assetFS.ReadFile("manifest.json")
	if err != nil {
		return nil, err
	}
	return decodeManifest(data)
}

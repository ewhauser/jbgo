package main

import (
	"embed"
	"encoding/json"
)

//go:embed manifest.json
var assetFS embed.FS

func loadManifest() (*manifest, error) {
	data, err := assetFS.ReadFile("manifest.json")
	if err != nil {
		return nil, err
	}
	var out manifest
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

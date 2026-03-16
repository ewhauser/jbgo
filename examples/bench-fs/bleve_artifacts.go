package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/blevesearch/bleve/v2"
	"github.com/ewhauser/gbash/examples/internal/blevefs"
	gbfs "github.com/ewhauser/gbash/fs"
)

func buildBleveArtifact(ctx context.Context, artifactDir string, files gbfs.InitialFiles) error {
	if err := os.MkdirAll(filepath.Dir(artifactDir), 0o755); err != nil {
		return fmt.Errorf("create bleve artifact parent: %w", err)
	}

	tmpDir := artifactDir + ".tmp"
	if err := os.RemoveAll(tmpDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove bleve artifact temp dir: %w", err)
	}

	index, err := bleve.New(tmpDir, blevefs.NewIndexMapping())
	if err != nil {
		return fmt.Errorf("create bleve artifact: %w", err)
	}

	closeIndex := func() error {
		if index == nil {
			return nil
		}
		err := index.Close()
		index = nil
		return err
	}
	defer func() {
		_ = closeIndex()
		_ = os.RemoveAll(tmpDir)
	}()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, gbfs.Clean(name))
	}
	sort.Strings(names)

	const batchSize = 256
	batch := index.NewBatch()
	pending := 0
	flush := func() error {
		if pending == 0 {
			return nil
		}
		if err := index.Batch(batch); err != nil {
			return err
		}
		batch = index.NewBatch()
		pending = 0
		return nil
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := benchmarkInitialFileContents(ctx, name, files[name])
		if err != nil {
			return err
		}
		if err := batch.Index(name, blevefs.NewDocument(name, data)); err != nil {
			return err
		}
		pending++
		if pending >= batchSize {
			if err := flush(); err != nil {
				return fmt.Errorf("flush bleve artifact batch: %w", err)
			}
		}
	}
	if err := flush(); err != nil {
		return fmt.Errorf("finalize bleve artifact batch: %w", err)
	}
	if err := closeIndex(); err != nil {
		return fmt.Errorf("close bleve artifact: %w", err)
	}

	if err := os.RemoveAll(artifactDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing bleve artifact: %w", err)
	}
	if err := os.Rename(tmpDir, artifactDir); err != nil {
		return fmt.Errorf("publish bleve artifact: %w", err)
	}
	return nil
}

func benchmarkInitialFileContents(ctx context.Context, name string, initial gbfs.InitialFile) ([]byte, error) {
	data := initial.Content
	if initial.Lazy == nil {
		return data, nil
	}

	loaded, err := initial.Lazy(ctx)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}
	return loaded, nil
}

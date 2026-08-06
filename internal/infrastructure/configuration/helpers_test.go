package configuration

import "github.com/samcharles93/archie-core/internal/config"

// These helpers unwrap Document for tests that only assert on the resulting
// settings. Tests covering provenance use the Loader methods directly.

func loadFile(path string) (config.Config, error) {
	return unwrap(New(nil).File(path))
}

func loadOverlay(basePath, overlayPath string) (config.Config, error) {
	return unwrap(New(nil).Overlay(basePath, overlayPath))
}

func unwrap(doc *Document, err error) (config.Config, error) {
	if err != nil {
		return config.Config{}, err
	}
	return doc.Config, nil
}

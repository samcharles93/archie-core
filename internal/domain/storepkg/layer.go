package storepkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

// FilesFromLayer reads an installed package's layer into its files, keyed by
// path. The fetch already validated the layer against Descriptor.Files and
// held it within the package size limit; this recovers the content the
// contributed names point at, plain or gzipped, whichever the package shipped.
func FilesFromLayer(layer []byte) (map[string][]byte, error) {
	reader := io.Reader(bytes.NewReader(layer))
	if len(layer) >= 2 && layer[0] == 0x1f && layer[1] == 0x8b {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("read package layer: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}
	files := map[string][]byte{}
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read package layer: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("read package file %q: %w", header.Name, err)
		}
		files[header.Name] = content
	}
}

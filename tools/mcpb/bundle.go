package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Binary is one binary GoReleaser built, as its artifacts.json lists it.
type Binary struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	Type   string `json:"type"`
}

// Binaries is every binary in GoReleaser's artifacts.json, in a stable order.
func Binaries(artifactsJSON []byte) ([]Binary, error) {
	var all []Binary
	if err := json.Unmarshal(artifactsJSON, &all); err != nil {
		return nil, fmt.Errorf("artifacts.json: %w", err)
	}
	var binaries []Binary
	for _, artifact := range all {
		if artifact.Type == "Binary" {
			binaries = append(binaries, artifact)
		}
	}
	if len(binaries) == 0 {
		return nil, fmt.Errorf("artifacts.json lists no binaries; run GoReleaser first")
	}
	sort.Slice(binaries, func(i, j int) bool {
		return binaries[i].GOOS+binaries[i].GOARCH < binaries[j].GOOS+binaries[j].GOARCH
	})
	return binaries, nil
}

// BundleName is the file a platform's bundle is published as.
func BundleName(version string, binary Binary) string {
	return fmt.Sprintf("mm-mcp_%s_%s_%s.mcpb", strings.TrimPrefix(version, "v"), binary.GOOS, binary.GOARCH)
}

// Bundle packs one binary into its platform's bundle in out, and returns the bundle's path.
func Bundle(template []byte, version string, binary Binary, out string) (string, error) {
	manifest, err := Manifest(template, version, binary.GOOS)
	if err != nil {
		return "", err
	}
	path := filepath.Join(out, BundleName(version, binary))
	file, err := os.Create(path) //nolint:gosec // a path under the output directory
	if err != nil {
		return "", err
	}
	archive := zip.NewWriter(file)
	if err := add(archive, "manifest.json", 0o644, func(w io.Writer) error {
		_, err := w.Write(manifest)
		return err
	}); err != nil {
		return "", closeAll(err, archive, file)
	}
	entry := "server/" + filepath.Base(binary.Path)
	if err := add(archive, entry, 0o755, func(w io.Writer) error {
		source, err := os.Open(binary.Path) //nolint:gosec // a path GoReleaser wrote
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }()
		_, err = io.Copy(w, source)
		return err
	}); err != nil {
		return "", closeAll(err, archive, file)
	}
	return path, closeAll(nil, archive, file)
}

// add writes one entry, with a Unix mode so the binary is executable once unpacked.
func add(archive *zip.Writer, name string, mode os.FileMode, write func(io.Writer) error) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	w, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	return write(w)
}

func closeAll(err error, archive *zip.Writer, file *os.File) error {
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

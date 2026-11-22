package main_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// savedImage is one platform's image as `docker save` writes it: its
// configuration and every layer, each file as that layer holds it, so a file
// a later layer removes is still seen.
type savedImage struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Entrypoint []string
		Cmd        []string
	} `json:"config"`
	layers []map[string]layerEntry
}

// layerEntry is one entry of a layer's archive.
type layerEntry struct {
	dir  bool
	data []byte // a regular file's content
	kind byte   // the archive's type flag
	mode int64
}

// saveImage saves the local image for linux/<arch> and reads it back.
func saveImage(t *testing.T, image, arch string) savedImage {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "image.tar")
	if out, err := exec.Command("docker", "save", "--platform", "linux/"+arch, "-o", archive, image).CombinedOutput(); err != nil {
		t.Fatalf("docker save %s for linux/%s: %v\n%s", image, arch, err, out)
	}
	files := readTar(t, archive)
	var manifest []struct {
		Config string
		Layers []string
	}
	if err := json.Unmarshal(files["manifest.json"].data, &manifest); err != nil || len(manifest) != 1 {
		t.Fatalf("the saved image's manifest.json: %v, %d images", err, len(manifest))
	}
	var img savedImage
	if err := json.Unmarshal(files[manifest[0].Config].data, &img); err != nil {
		t.Fatalf("the image's configuration: %v", err)
	}
	for _, l := range manifest[0].Layers {
		img.layers = append(img.layers, readLayer(t, files[l].data))
	}
	return img
}

// readTar reads the archive at path into its entries by name.
func readTar(t *testing.T, path string) map[string]layerEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return entries(t, f)
}

// readLayer reads one layer's archive, compressed or not.
func readLayer(t *testing.T, data []byte) map[string]layerEntry {
	t.Helper()
	var r io.Reader = bytes.NewReader(data)
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		z, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		r = z
	}
	return entries(t, r)
}

func entries(t *testing.T, r io.Reader) map[string]layerEntry {
	t.Helper()
	out := map[string]layerEntry{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		e := layerEntry{dir: h.Typeflag == tar.TypeDir, kind: h.Typeflag, mode: h.Mode}
		if h.Typeflag == tar.TypeReg {
			if e.data, err = io.ReadAll(tr); err != nil {
				t.Fatal(err)
			}
		}
		out[name] = e
	}
}

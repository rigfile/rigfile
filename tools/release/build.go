package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Artifact is one built archive.
type Artifact struct {
	Target Target
	Name   string // file name
	Path   string
	SHA256 string
	Binary string // path of the built binary (kept for packaging)
}

func archiveName(version string, t Target) string {
	ext := ".tar.gz"
	if t.OS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("rigfile_%s_%s_%s%s", version, t.OS, t.Arch, ext)
}

func binName(t Target) string {
	if t.OS == "windows" {
		return "rigfile.exe"
	}
	return "rigfile"
}

func buildArchive(cfg Config, t Target) (Artifact, error) {
	bin := filepath.Join(cfg.Out, ".build", t.OS+"_"+t.Arch, binName(t))
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return Artifact{}, err
	}
	ld := "-s -w -X main.version=" + cfg.Version
	if cfg.PubKey != "" {
		ld += " -X " + cfg.Module + "/internal/selfupdate.PublicKey=" + cfg.PubKey
	}
	c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ld, "-o", bin, cfg.Package)
	c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.OS, "GOARCH="+t.Arch)
	if out, err := c.CombinedOutput(); err != nil {
		return Artifact{}, fmt.Errorf("go build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return Artifact{}, err
	}
	name := archiveName(cfg.Version, t)
	dir := strings.TrimSuffix(strings.TrimSuffix(name, ".tar.gz"), ".zip")
	mt := time.Unix(cfg.Epoch, 0)
	path := filepath.Join(cfg.Out, name)
	f, err := os.Create(path)
	if err != nil {
		return Artifact{}, err
	}
	if t.OS == "windows" {
		zw := zip.NewWriter(f)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: dir + "/" + binName(t), Method: zip.Deflate, Modified: mt})
		if err != nil {
			return Artifact{}, err
		}
		if _, err := w.Write(data); err != nil {
			return Artifact{}, err
		}
		if err := zw.Close(); err != nil {
			return Artifact{}, err
		}
	} else {
		gw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
		tw := tar.NewWriter(gw)
		if err := tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: mt}); err != nil {
			return Artifact{}, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: dir + "/" + binName(t), Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(data)), ModTime: mt}); err != nil {
			return Artifact{}, err
		}
		if _, err := tw.Write(data); err != nil {
			return Artifact{}, err
		}
		if err := tw.Close(); err != nil {
			return Artifact{}, err
		}
		if err := gw.Close(); err != nil {
			return Artifact{}, err
		}
	}
	if err := f.Close(); err != nil {
		return Artifact{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, err
	}
	sum := sha256.Sum256(b)
	return Artifact{Target: t, Name: name, Path: path, SHA256: hex.EncodeToString(sum[:]), Binary: bin}, nil
}

func writeChecksums(cfg Config, arts []Artifact) error {
	sort.Slice(arts, func(i, j int) bool { return arts[i].Name < arts[j].Name })
	var b strings.Builder
	for _, a := range arts {
		fmt.Fprintf(&b, "%s  %s\n", a.SHA256, a.Name)
	}
	return os.WriteFile(filepath.Join(cfg.Out, "SHA256SUMS"), []byte(b.String()), 0o644)
}

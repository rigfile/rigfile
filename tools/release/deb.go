package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// buildDeb writes rigfile_<version>_<arch>.deb: an ar archive of debian-binary, control.tar.gz and data.tar.gz.
func buildDeb(cfg Config, t Target) (Artifact, error) {
	bin, err := os.ReadFile(filepath.Join(cfg.Out, ".build", t.OS+"_"+t.Arch, binName(t)))
	if err != nil {
		return Artifact{}, err
	}
	mt := time.Unix(cfg.Epoch, 0)
	tarball := func(files []tf) ([]byte, error) {
		var b bytes.Buffer
		gw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
		tw := tar.NewWriter(gw)
		for _, f := range files {
			if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: f.typ, Mode: f.mode, Size: int64(len(f.data)), ModTime: mt, Uname: "root", Gname: "root"}); err != nil {
				return nil, err
			}
			if _, err := tw.Write(f.data); err != nil {
				return nil, err
			}
		}
		if err := tw.Close(); err != nil {
			return nil, err
		}
		if err := gw.Close(); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	control := fmt.Sprintf("Package: rigfile\nVersion: %s\nArchitecture: %s\nMaintainer: Rigfile maintainers <bytebuilderslab@gmail.com>\n"+
		"Installed-Size: %d\nSection: utils\nPriority: optional\nHomepage: https://github.com/%s\n"+
		"Description: one manifest for every AI coding tool\n Rigfile applies a rig (instructions, skills, MCP servers, hooks, permissions) to Claude Code,\n Codex, Gemini CLI and Cursor, and keeps secrets out of the agent's reach.\n",
		cfg.Version, t.Arch, (len(bin)+1023)/1024, cfg.Repo)
	ctl, err := tarball([]tf{{"./control", tar.TypeReg, 0o644, []byte(control)}})
	if err != nil {
		return Artifact{}, err
	}
	data, err := tarball([]tf{
		{"./usr/", tar.TypeDir, 0o755, nil}, {"./usr/bin/", tar.TypeDir, 0o755, nil},
		{"./usr/bin/rigfile", tar.TypeReg, 0o755, bin},
	})
	if err != nil {
		return Artifact{}, err
	}
	var ar bytes.Buffer
	ar.WriteString("!<arch>\n")
	for _, m := range []struct {
		name string
		data []byte
	}{{"debian-binary", []byte("2.0\n")}, {"control.tar.gz", ctl}, {"data.tar.gz", data}} {
		fmt.Fprintf(&ar, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", m.name+"/", cfg.Epoch, 0, 0, "100644", len(m.data))
		ar.Write(m.data)
		if len(m.data)%2 == 1 {
			ar.WriteByte('\n')
		}
	}
	name := fmt.Sprintf("rigfile_%s_%s.deb", cfg.Version, t.Arch)
	path := filepath.Join(cfg.Out, name)
	if err := os.WriteFile(path, ar.Bytes(), 0o644); err != nil {
		return Artifact{}, err
	}
	h := sha256.Sum256(ar.Bytes())
	return Artifact{Target: t, Name: name, Path: path, SHA256: hex.EncodeToString(h[:])}, nil
}

type tf struct {
	name string
	typ  byte
	mode int64
	data []byte
}

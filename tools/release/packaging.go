package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (c Config) url(name string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", c.Repo, c.Version, name)
}

func find(arts []Artifact, os_, arch string) (Artifact, bool) {
	for _, a := range arts {
		if a.Target.OS == os_ && a.Target.Arch == arch && !strings.HasSuffix(a.Name, ".deb") {
			return a, true
		}
	}
	return Artifact{}, false
}

func write(root, rel, body string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o644)
}

// renderPackaging writes the package-manager manifests into <out>/packaging. Every hash comes from the archives just
// built; nothing is guessed. The names (Rigfile.Rigfile, org rigfile) and the licence are the owner's decisions of 2026-09-26.
func renderPackaging(cfg Config, arts []Artifact) error {
	root := filepath.Join(cfg.Out, "packaging")
	if err := renderHomebrew(cfg, arts, root); err != nil {
		return err
	}
	if err := renderScoop(cfg, arts, root); err != nil {
		return err
	}
	if err := renderWinget(cfg, arts, root); err != nil {
		return err
	}
	if err := renderNPM(cfg, arts, root); err != nil {
		return err
	}
	return renderWheels(cfg, arts, root)
}

func renderHomebrew(cfg Config, arts []Artifact, root string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "class Rigfile < Formula\n  desc \"One manifest for every AI coding tool, with secrets kept out of the agent's reach\"\n  homepage \"https://github.com/%s\"\n  version \"%s\"\n  license \"Apache-2.0\"\n\n", cfg.Repo, cfg.Version)
	for _, blk := range [][2]string{{"macos", "darwin"}, {"linux", "linux"}} {
		fmt.Fprintf(&b, "  on_%s do\n", blk[0])
		first := true
		for _, p := range [][2]string{{"arm", "arm64"}, {"intel", "amd64"}} {
			a, ok := find(arts, blk[1], p[1])
			if !ok {
				continue
			}
			if first {
				fmt.Fprintf(&b, "    if Hardware::CPU.%s?\n", p[0])
				first = false
			} else {
				b.WriteString("    else\n")
			}
			fmt.Fprintf(&b, "      url \"%s\"\n      sha256 \"%s\"\n", cfg.url(a.Name), a.SHA256)
		}
		if !first {
			b.WriteString("    end\n")
		}
		b.WriteString("  end\n\n")
	}
	b.WriteString("  def install\n    bin.install \"rigfile\"\n  end\n\n  test do\n    assert_match version.to_s, shell_output(\"#{bin}/rigfile version\")\n  end\nend\n")
	return write(root, "homebrew/rigfile.rb", b.String())
}

func renderScoop(cfg Config, arts []Artifact, root string) error {
	arch := map[string]any{}
	for _, p := range [][2]string{{"64bit", "amd64"}, {"arm64", "arm64"}} {
		if a, ok := find(arts, "windows", p[1]); ok {
			arch[p[0]] = map[string]any{"url": cfg.url(a.Name), "hash": a.SHA256, "extract_dir": strings.TrimSuffix(a.Name, ".zip")}
		}
	}
	m := map[string]any{
		"version": cfg.Version, "description": "One manifest for every AI coding tool, with secrets kept out of the agent's reach",
		"homepage": "https://github.com/" + cfg.Repo, "license": "Apache-2.0", "architecture": arch, "bin": "rigfile.exe",
		"checkver": map[string]any{"github": "https://github.com/" + cfg.Repo},
		"autoupdate": map[string]any{"architecture": map[string]any{
			"64bit": map[string]any{"url": "https://github.com/" + cfg.Repo + "/releases/download/v$version/rigfile_$version_windows_amd64.zip", "extract_dir": "rigfile_$version_windows_amd64"},
			"arm64": map[string]any{"url": "https://github.com/" + cfg.Repo + "/releases/download/v$version/rigfile_$version_windows_arm64.zip", "extract_dir": "rigfile_$version_windows_arm64"},
		}},
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return write(root, "scoop/rigfile.json", string(b)+"\n")
}

func renderWinget(cfg Config, arts []Artifact, root string) error {
	id := "Rigfile.Rigfile" // PackageIdentifier is Publisher.Package
	head := fmt.Sprintf("PackageIdentifier: %s\nPackageVersion: %s\n", id, cfg.Version)
	dir := "winget/manifests/" + strings.ToLower(id[:1]) + "/" + strings.ReplaceAll(id, ".", "/") + "/" + cfg.Version + "/" // winget-pkgs layout: manifests/<first letter>/<Publisher>/<Package>/<version>
	if err := write(root, dir+id+".yaml", head+"DefaultLocale: en-US\nManifestType: version\nManifestVersion: 1.6.0\n"); err != nil {
		return err
	}
	var inst strings.Builder
	inst.WriteString(head + "InstallerType: zip\nNestedInstallerType: portable\nInstallers:\n")
	for _, p := range [][2]string{{"x64", "amd64"}, {"arm64", "arm64"}} {
		a, ok := find(arts, "windows", p[1])
		if !ok {
			continue
		}
		fmt.Fprintf(&inst, "  - Architecture: %s\n    InstallerUrl: %s\n    InstallerSha256: %s\n    NestedInstallerFiles:\n      - RelativeFilePath: %s/rigfile.exe\n        PortableCommandAlias: rigfile\n",
			p[0], cfg.url(a.Name), strings.ToUpper(a.SHA256), strings.TrimSuffix(a.Name, ".zip"))
	}
	inst.WriteString("ManifestType: installer\nManifestVersion: 1.6.0\n")
	if err := write(root, dir+id+".installer.yaml", inst.String()); err != nil {
		return err
	}
	loc := head + "PackageLocale: en-US\nPublisher: Rigfile\nPackageName: Rigfile\nLicense: Apache-2.0\n" +
		"ShortDescription: One manifest for every AI coding tool, with secrets kept out of the agent's reach\nPackageUrl: https://github.com/" + cfg.Repo + "\nManifestType: defaultLocale\nManifestVersion: 1.6.0\n"
	return write(root, dir+id+".locale.en-US.yaml", loc)
}

func renderNPM(cfg Config, arts []Artifact, root string) error {
	sums := map[string]string{}
	for _, a := range arts {
		if !strings.HasSuffix(a.Name, ".deb") {
			sums[a.Name] = a.SHA256
		}
	}
	pkg := map[string]any{
		"name": "rigfile", "version": cfg.Version, "description": "One manifest for every AI coding tool, with secrets kept out of the agent's reach",
		"bin": map[string]string{"rigfile": "bin/rigfile.js"}, "scripts": map[string]string{"postinstall": "node install.js"},
		"files": []string{"bin", "install.js", "checksums.json"}, "engines": map[string]string{"node": ">=18"},
		"repository": "github:" + cfg.Repo, "license": "Apache-2.0",
	}
	pb, _ := json.MarshalIndent(pkg, "", "  ")
	cb, _ := json.MarshalIndent(map[string]any{"version": cfg.Version, "base": fmt.Sprintf("https://github.com/%s/releases/download/v%s/", cfg.Repo, cfg.Version), "sha256": sums}, "", "  ")
	for rel, body := range map[string]string{"npm/package.json": string(pb) + "\n", "npm/checksums.json": string(cb) + "\n", "npm/install.js": npmInstall, "npm/bin/rigfile.js": npmLauncher} {
		if err := write(root, rel, body); err != nil {
			return err
		}
	}
	return nil
}

const npmLauncher = `#!/usr/bin/env node
// Runs the rigfile binary that install.js downloaded and verified.
const { spawnSync } = require("child_process");
const path = require("path");
const bin = path.join(__dirname, process.platform === "win32" ? "rigfile.exe" : "rigfile-bin");
const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (r.error) { console.error("rigfile: the binary is missing; reinstall the package (" + r.error.message + ")"); process.exit(1); }
process.exit(r.status === null ? 1 : r.status);
`

const npmInstall = `// postinstall: download this version's archive from the GitHub release, refuse it unless its SHA-256 matches the
// checksum that was baked into this package when it was published, and unpack the binary next to the launcher.
"use strict";
const https = require("https");
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const zlib = require("zlib");
const cs = require("./checksums.json");

const osName = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
if (!osName || !arch) { console.error("rigfile: no build for " + process.platform + "/" + process.arch); process.exit(1); }
const ext = osName === "windows" ? ".zip" : ".tar.gz";
const name = "rigfile_" + cs.version + "_" + osName + "_" + arch + ext;
const want = cs.sha256[name];
if (!want) { console.error("rigfile: " + name + " is not part of this release"); process.exit(1); }

function get(url, hops) {
  return new Promise((resolve, reject) => {
    if (hops > 5) return reject(new Error("too many redirects"));
    const u = new URL(url);
    if (u.protocol !== "https:" || !(u.hostname === "github.com" || u.hostname.endsWith(".githubusercontent.com"))) return reject(new Error("refusing " + u.host));
    https.get(u, { headers: { "User-Agent": "rigfile-npm" } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) { res.resume(); return resolve(get(new URL(res.headers.location, u).toString(), hops + 1)); }
      if (res.statusCode !== 200) { res.resume(); return reject(new Error("HTTP " + res.statusCode)); }
      const chunks = []; let n = 0;
      res.on("data", (c) => { n += c.length; if (n > 200 << 20) { res.destroy(new Error("too large")); } else chunks.push(c); });
      res.on("end", () => resolve(Buffer.concat(chunks)));
      res.on("error", reject);
    }).on("error", reject);
  });
}

// minimal tar / zip readers for a single known file
function fromTarGz(buf, base) {
  const t = zlib.gunzipSync(buf);
  for (let off = 0; off + 512 <= t.length;) {
    const h = t.subarray(off, off + 512);
    if (h.every((b) => b === 0)) break;
    const nm = h.toString("utf8", 0, 100).replace(/\0.*$/, "");
    const size = parseInt(h.toString("utf8", 124, 136).replace(/\0.*$/, "").trim() || "0", 8);
    const type = String.fromCharCode(h[156]);
    if ((type === "0" || type === "\0") && path.posix.basename(nm) === base) return t.subarray(off + 512, off + 512 + size);
    off += 512 + Math.ceil(size / 512) * 512;
  }
  throw new Error(base + " not found in the archive");
}
function fromZip(buf, base) {
  for (let off = 0; off + 30 <= buf.length;) {
    if (buf.readUInt32LE(off) !== 0x04034b50) break;
    const method = buf.readUInt16LE(off + 8), csz = buf.readUInt32LE(off + 18), nl = buf.readUInt16LE(off + 26), el = buf.readUInt16LE(off + 28);
    const nm = buf.toString("utf8", off + 30, off + 30 + nl);
    const data = buf.subarray(off + 30 + nl + el, off + 30 + nl + el + csz);
    if (path.posix.basename(nm) === base) return method === 0 ? data : zlib.inflateRawSync(data);
    off += 30 + nl + el + csz;
  }
  throw new Error(base + " not found in the archive");
}

get(cs.base + name, 0).then((buf) => {
  const got = crypto.createHash("sha256").update(buf).digest("hex");
  if (got !== want) throw new Error("checksum mismatch for " + name + " (expected " + want.slice(0, 12) + ", got " + got.slice(0, 12) + ")");
  const bin = osName === "windows" ? fromZip(buf, "rigfile.exe") : fromTarGz(buf, "rigfile");
  const dest = path.join(__dirname, "bin", osName === "windows" ? "rigfile.exe" : "rigfile-bin");
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(dest, bin, { mode: 0o755 });
}).catch((e) => { console.error("rigfile: install failed: " + e.message); process.exit(1); });
`

// ---- pip wheels ----------------------------------------------------------------------------------------------------

func wheelTag(t Target) (string, bool) {
	switch t.OS + "/" + t.Arch {
	case "linux/amd64":
		return "manylinux_2_17_x86_64", true
	case "linux/arm64":
		return "manylinux_2_17_aarch64", true
	case "darwin/amd64":
		return "macosx_10_9_x86_64", true
	case "darwin/arm64":
		return "macosx_11_0_arm64", true
	case "windows/amd64":
		return "win_amd64", true
	case "windows/arm64":
		return "win_arm64", true
	}
	return "", false
}

const pyInit = `"""Launcher for the Rigfile binary bundled in this wheel."""
import os
import subprocess
import sys


def main():
    exe = os.path.join(os.path.dirname(os.path.abspath(__file__)), "bin", "rigfile.exe" if os.name == "nt" else "rigfile")
    if os.name != "nt":
        os.execv(exe, [exe] + sys.argv[1:])
    sys.exit(subprocess.call([exe] + sys.argv[1:]))
`

// renderWheels builds one platform wheel per target: the verified binary is inside, so nothing is downloaded at
// install time (unlike npm).
func renderWheels(cfg Config, arts []Artifact, root string) error {
	for _, a := range arts {
		if strings.HasSuffix(a.Name, ".deb") || a.Binary == "" {
			continue
		}
		tag, ok := wheelTag(a.Target)
		if !ok {
			continue
		}
		bin, err := os.ReadFile(a.Binary)
		if err != nil {
			return err
		}
		dist := "rigfile-" + cfg.Version
		meta := fmt.Sprintf("Metadata-Version: 2.1\nName: rigfile\nVersion: %s\nSummary: One manifest for every AI coding tool, with secrets kept out of the agent's reach\nHome-page: https://github.com/%s\nLicense: Apache-2.0\nRequires-Python: >=3.8\n", cfg.Version, cfg.Repo)
		wheel := fmt.Sprintf("Wheel-Version: 1.0\nGenerator: rigfile-release\nRoot-Is-Purelib: false\nTag: py3-none-%s\n", tag)
		entry := "[console_scripts]\nrigfile = rigfile:main\n"
		files := []struct {
			name string
			data []byte
			mode uint32
		}{
			{"rigfile/__init__.py", []byte(pyInit), 0o644},
			{"rigfile/bin/" + binName(a.Target), bin, 0o755},
			{dist + ".dist-info/METADATA", []byte(meta), 0o644},
			{dist + ".dist-info/WHEEL", []byte(wheel), 0o644},
			{dist + ".dist-info/entry_points.txt", []byte(entry), 0o644},
		}
		var record strings.Builder
		for _, f := range files {
			h := sha256.Sum256(f.data)
			fmt.Fprintf(&record, "%s,sha256=%s,%d\n", f.name, base64.RawURLEncoding.EncodeToString(h[:]), len(f.data))
		}
		fmt.Fprintf(&record, "%s.dist-info/RECORD,,\n", dist)
		files = append(files, struct {
			name string
			data []byte
			mode uint32
		}{dist + ".dist-info/RECORD", []byte(record.String()), 0o644})
		sort.SliceStable(files, func(i, j int) bool { return files[i].name < files[j].name })

		out := filepath.Join(root, "pip", fmt.Sprintf("rigfile-%s-py3-none-%s.whl", cfg.Version, tag))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		zw := zip.NewWriter(f)
		for _, fl := range files {
			h := &zip.FileHeader{Name: fl.name, Method: zip.Deflate, Modified: time.Unix(cfg.Epoch, 0)}
			h.SetMode(os.FileMode(fl.mode))
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			if _, err := w.Write(fl.data); err != nil {
				return err
			}
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

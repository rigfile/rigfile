package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/minisign"
)

func hostTarget() Target { return Target{runtime.GOOS, runtime.GOARCH} }

func run(t *testing.T, cfg Config) {
	t.Helper()
	cfg.Package = "../../cmd/rigfile" // relative to this package's directory, where `go test` runs
	cfg.Scripts = "../../scripts"
	if err := Run(cfg, ""); err != nil {
		t.Fatal(err)
	}
}

func testKey() minisign.KeyPair {
	return minisign.KeyPair{ID: [8]byte{1, 1, 1, 1, 2, 2, 2, 2}, Private: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))}
}

func pubLine(k minisign.KeyPair) string {
	l := strings.Split(strings.TrimSpace(k.Public()), "\n")
	return l[len(l)-1]
}

func TestReleaseBuildsVerifiableReproducibleArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles")
	}
	k := testKey()
	// the host target may equal one of the cross targets (windows/amd64 on a Windows runner): build each once
	var targets []Target
	seen := map[Target]bool{}
	for _, tg := range []Target{hostTarget(), {"linux", "amd64"}, {"windows", "amd64"}} {
		if !seen[tg] {
			seen[tg] = true
			targets = append(targets, tg)
		}
	}
	mk := func() (string, Config) {
		cfg := Config{Version: "9.9.9", Out: filepath.Join(t.TempDir(), "dist"), Targets: targets, PubKey: pubLine(k), Module: "github.com/rigfile/rigfile",
			Repo: "example-owner/rigfile", Epoch: 1700000000}
		run(t, cfg)
		return cfg.Out, cfg
	}
	out, _ := mk()
	out2, _ := mk()

	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	sums2, _ := os.ReadFile(filepath.Join(out2, "SHA256SUMS"))
	if string(sums) != string(sums2) {
		t.Fatalf("the build is not reproducible:\n%s\n%s", sums, sums2)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		f := strings.Fields(l)
		b, err := os.ReadFile(filepath.Join(out, f[1]))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != f[0] {
			t.Errorf("%s does not match SHA256SUMS", f[1])
		}
	}
	for _, want := range []string{"rigfile_9.9.9_linux_amd64.tar.gz", "rigfile_9.9.9_windows_amd64.zip", "rigfile_9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH, "rigfile_9.9.9_amd64.deb"} {
		if !strings.Contains(string(sums), want) {
			t.Errorf("SHA256SUMS is missing %s:\n%s", want, sums)
		}
	}
	if _, err := os.Stat(filepath.Join(out, ".build")); !os.IsNotExist(err) {
		t.Error("the build directory must be removed")
	}

	// the host archive holds a working binary that reports the version and carries the embedded verification key
	arch := filepath.Join(out, "rigfile_9.9.9_"+runtime.GOOS+"_"+runtime.GOARCH)
	bin := extract(t, arch, runtime.GOOS)
	exe := filepath.Join(t.TempDir(), "rigfile"+map[bool]string{true: ".exe"}[runtime.GOOS == "windows"])
	if err := os.WriteFile(exe, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if o, err := exec.Command(exe, "version").CombinedOutput(); err != nil || strings.TrimSpace(string(o)) != "rigfile 9.9.9" {
		t.Fatalf("%q %v", o, err)
	}
	msg := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(msg, []byte("hello"), 0o644)
	_ = os.WriteFile(msg+".minisig", []byte(k.Sign([]byte("hello"), "rigfile v9.9.9")), 0o644)
	if o, err := exec.Command(exe, "verify-signature", msg).CombinedOutput(); err != nil || !strings.Contains(string(o), "signature is valid") {
		t.Fatalf("the embedded key was not compiled in: %q %v", o, err)
	}

	sh := read(t, filepath.Join(out, "install.sh"))
	if !strings.Contains(sh, `PUBKEY="`+pubLine(k)+`"`) || !strings.Contains(sh, `REPO="example-owner/rigfile"`) || strings.Contains(sh, "__RIGFILE_REPO__") || strings.Contains(sh, "__RIGFILE_PUBKEY__") {
		t.Errorf("install.sh was not rendered:\n%s", sh[:600])
	}
	if !strings.Contains(read(t, filepath.Join(out, "install.ps1")), "'"+pubLine(k)+"'") {
		t.Error("install.ps1 was not rendered")
	}
	checkPackaging(t, out)
	checkDeb(t, filepath.Join(out, "rigfile_9.9.9_amd64.deb"))
	checkWheel(t, out, exe, len(targets))
}

func extract(t *testing.T, base, goos string) []byte {
	t.Helper()
	if goos == "windows" {
		zr, err := zip.OpenReader(base + ".zip")
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		rc, _ := zr.File[0].Open()
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return b
	}
	f, err := os.Open(base + ".tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, _ := gzip.NewReader(f)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatal("no binary in the archive")
		}
		if h.Typeflag == tar.TypeReg {
			if h.Mode&0o111 == 0 {
				t.Error("the binary must be executable")
			}
			b, _ := io.ReadAll(tr)
			return b
		}
	}
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkPackaging(t *testing.T, out string) {
	sums := read(t, filepath.Join(out, "SHA256SUMS"))
	hashOf := func(name string) string {
		for _, l := range strings.Split(sums, "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[1] == name {
				return f[0]
			}
		}
		t.Fatalf("no checksum for %s", name)
		return ""
	}
	brew := read(t, filepath.Join(out, "packaging/homebrew/rigfile.rb"))
	if !strings.Contains(brew, "example-owner/rigfile/releases/download/v9.9.9/rigfile_9.9.9_linux_amd64.tar.gz") || !strings.Contains(brew, hashOf("rigfile_9.9.9_linux_amd64.tar.gz")) ||
		!strings.Contains(brew, "on_linux do") || !strings.Contains(brew, `bin.install "rigfile"`) {
		t.Errorf("homebrew formula:\n%s", brew)
	}
	var scoop map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(out, "packaging/scoop/rigfile.json"))), &scoop); err != nil {
		t.Fatal(err)
	}
	a := scoop["architecture"].(map[string]any)["64bit"].(map[string]any)
	if scoop["version"] != "9.9.9" || a["hash"] != hashOf("rigfile_9.9.9_windows_amd64.zip") || a["extract_dir"] != "rigfile_9.9.9_windows_amd64" {
		t.Errorf("scoop: %v", scoop)
	}
	inst := read(t, filepath.Join(out, "packaging/winget/manifests/r/Rigfile/Rigfile/9.9.9/Rigfile.Rigfile.installer.yaml"))
	if !strings.Contains(inst, strings.ToUpper(hashOf("rigfile_9.9.9_windows_amd64.zip"))) || !strings.Contains(inst, "PortableCommandAlias: rigfile") || !strings.Contains(inst, "ManifestVersion: 1.6.0") {
		t.Errorf("winget installer:\n%s", inst)
	}
	var cs struct {
		Version string
		Base    string
		SHA256  map[string]string
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(out, "packaging/npm/checksums.json"))), &cs); err != nil {
		t.Fatal(err)
	}
	if cs.SHA256["rigfile_9.9.9_linux_amd64.tar.gz"] != hashOf("rigfile_9.9.9_linux_amd64.tar.gz") || !strings.HasPrefix(cs.Base, "https://github.com/example-owner/rigfile/") {
		t.Errorf("npm checksums: %+v", cs)
	}
	if _, err := exec.LookPath("node"); err == nil {
		// the launcher and installer must at least parse
		for _, f := range []string{"install.js", "bin/rigfile.js"} {
			if o, err := exec.Command("node", "--check", filepath.Join(out, "packaging/npm", f)).CombinedOutput(); err != nil {
				t.Errorf("node --check %s: %v\n%s", f, err, o)
			}
		}
	}
}

func checkDeb(t *testing.T, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("!<arch>\n")) {
		t.Fatal("not an ar archive")
	}
	members := map[string][]byte{}
	for off := 8; off+60 <= len(b); {
		name := strings.TrimRight(strings.TrimSpace(string(b[off:off+16])), "/")
		var size int
		if _, err := fmtSscan(string(b[off+48:off+58]), &size); err != nil || string(b[off+58:off+60]) != "`\n" {
			t.Fatalf("bad ar header at %d", off)
		}
		members[name] = b[off+60 : off+60+size]
		off += 60 + size + size%2
	}
	if string(members["debian-binary"]) != "2.0\n" {
		t.Fatal("debian-binary")
	}
	ctl := tarNames(t, members["control.tar.gz"])
	if !strings.Contains(ctl["./control"], "Package: rigfile") || !strings.Contains(ctl["./control"], "Version: 9.9.9") || !strings.Contains(ctl["./control"], "Architecture: amd64") {
		t.Fatalf("control: %q", ctl["./control"])
	}
	if data := tarNames(t, members["data.tar.gz"]); len(data["./usr/bin/rigfile"]) < 1000 {
		t.Fatal("the binary is not in /usr/bin")
	}
}

func fmtSscan(s string, n *int) (int, error) {
	v := 0
	for _, c := range strings.TrimSpace(s) {
		v = v*10 + int(c-'0')
	}
	*n = v
	return 1, nil
}

func tarNames(t *testing.T, gzData []byte) map[string]string {
	gz, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err != nil {
			return out
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

func checkWheel(t *testing.T, out, hostExe string, want int) {
	whl, _ := filepath.Glob(filepath.Join(out, "packaging/pip/*.whl"))
	if len(whl) != want {
		t.Fatalf("wheels: %v", whl)
	}
	tag, _ := wheelTag(hostTarget())
	host := filepath.Join(out, "packaging/pip/rigfile-9.9.9-py3-none-"+tag+".whl")
	if _, err := os.Stat(host); err != nil {
		t.Fatal(err)
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	target := t.TempDir()
	if o, err := exec.Command(py, "-m", "pip", "install", "--no-index", "--no-deps", "--target", target, host).CombinedOutput(); err != nil {
		t.Skipf("pip cannot install offline here: %v\n%s", err, o)
	}
	c := exec.Command(py, "-c", "import rigfile, sys; sys.argv=['rigfile','version']; rigfile.main()")
	c.Env = append(os.Environ(), "PYTHONPATH="+target)
	o, err := c.CombinedOutput()
	if err != nil || strings.TrimSpace(string(o)) != "rigfile 9.9.9" {
		t.Fatalf("the wheel launcher: %q %v", o, err)
	}
	if _, err := os.Stat(filepath.Join(target, "rigfile-9.9.9.dist-info", "RECORD")); err != nil {
		t.Fatal("wheel metadata missing")
	}
}

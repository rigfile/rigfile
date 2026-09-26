package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/minisign"
)

func key(seed byte) minisign.KeyPair {
	s := bytes.Repeat([]byte{seed}, 32)
	return minisign.KeyPair{ID: [8]byte{9, 9, 9, 9, 9, 9, 9, seed}, Private: ed25519.NewKeyFromSeed(s)}
}

func targz(t *testing.T, name, body string) []byte {
	var b bytes.Buffer
	gw := gzip.NewWriter(&b)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "rigfile_1.1.0_linux_amd64/" + name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gw.Close()
	return b.Bytes()
}

func zipOf(t *testing.T, name, body string) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create(name)
	_, _ = w.Write([]byte(body))
	_ = zw.Close()
	return b.Bytes()
}

type release struct {
	srv            *httptest.Server
	tag            string
	archives       map[string][]byte // name -> bytes as served
	sums           string
	sig            string
	missingSig     bool
	redirectAssets string
}

func newRelease(t *testing.T, k minisign.KeyPair, tag string, archives map[string][]byte) *release {
	r := &release{tag: tag, archives: archives}
	var lines []string
	for name, data := range archives {
		s := sha256.Sum256(data)
		lines = append(lines, hex.EncodeToString(s[:])+"  "+name)
	}
	r.sums = strings.Join(lines, "\n") + "\n"
	r.sig = k.Sign([]byte(r.sums), "rigfile "+tag)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q}`, r.tag)
	})
	mux.HandleFunc("/dl/"+tag+"/", func(w http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/dl/"+tag+"/")
		switch {
		case name == "SHA256SUMS":
			fmt.Fprint(w, r.sums)
		case name == "SHA256SUMS.minisig":
			if r.missingSig {
				http.NotFound(w, req)
				return
			}
			fmt.Fprint(w, r.sig)
		case r.archives[name] != nil:
			if r.redirectAssets != "" {
				http.Redirect(w, req, r.redirectAssets, http.StatusFound)
				return
			}
			_, _ = w.Write(r.archives[name])
		default:
			http.NotFound(w, req)
		}
	})
	r.srv = httptest.NewTLSServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *release) opts(t *testing.T, k minisign.KeyPair, exe, current string) Options {
	u, _ := url.Parse(r.srv.URL)
	c := r.srv.Client()
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return Options{Current: current, GOOS: "linux", GOARCH: "amd64", Exe: exe, PubKey: k.Public(), Client: c,
		APIURL: r.srv.URL, DownloadBase: r.srv.URL + "/dl", AllowedHosts: []string{u.Host}}
}

func exeFile(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "rigfile")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdatesAfterVerifyingSignatureAndChecksum(t *testing.T) {
	k := key(1)
	rel := newRelease(t, k, "v1.1.0", map[string][]byte{"rigfile_1.1.0_linux_amd64.tar.gz": targz(t, "rigfile", "NEW BINARY")})
	exe := exeFile(t, "OLD BINARY")
	res, err := Run(t.Context(), rel.opts(t, k, exe, "1.0.0"))
	if err != nil || !res.Updated || res.Latest != "v1.1.0" {
		t.Fatalf("%+v %v", res, err)
	}
	if read(t, exe) != "NEW BINARY" || read(t, exe+".old") != "OLD BINARY" {
		t.Fatal("the binary was not swapped")
	}
	if st, _ := os.Stat(exe); runtime.GOOS != "windows" && st.Mode()&0o100 == 0 {
		t.Fatal("the new binary must be executable")
	}
	// already current
	res, err = Run(t.Context(), rel.opts(t, k, exe, "1.1.0"))
	if err != nil || !res.UpToDate {
		t.Fatalf("%+v %v", res, err)
	}
	// check-only verifies but replaces nothing
	exe2 := exeFile(t, "OLD BINARY")
	o := rel.opts(t, k, exe2, "1.0.0")
	o.CheckOnly = true
	if res, err = Run(t.Context(), o); err != nil || res.Updated || read(t, exe2) != "OLD BINARY" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestWindowsZip(t *testing.T) {
	k := key(1)
	rel := newRelease(t, k, "v1.1.0", map[string][]byte{"rigfile_1.1.0_windows_amd64.zip": zipOf(t, "rigfile.exe", "WIN BINARY")})
	exe := exeFile(t, "OLD")
	o := rel.opts(t, k, exe, "1.0.0")
	o.GOOS = "windows"
	if res, err := Run(t.Context(), o); err != nil || !res.Updated || read(t, exe) != "WIN BINARY" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRefusesEveryUntrustworthyRelease(t *testing.T) {
	k := key(1)
	arch := map[string][]byte{"rigfile_1.1.0_linux_amd64.tar.gz": targz(t, "rigfile", "NEW BINARY")}

	cases := map[string]func(r *release, o *Options){
		"tampered archive": func(r *release, o *Options) {
			r.archives["rigfile_1.1.0_linux_amd64.tar.gz"] = targz(t, "rigfile", "EVIL")
		},
		"wrong signing key": func(r *release, o *Options) { o.PubKey = key(2).Public() },
		"unsigned release":  func(r *release, o *Options) { r.missingSig = true },
		"edited checksums": func(r *release, o *Options) {
			r.sums = strings.Replace(r.sums, "  ", "  ", 1) + "0000  extra\n"
		},
		"signature replayed from an older tag": func(r *release, o *Options) { r.sig = k.Sign([]byte(r.sums), "rigfile v1.0.5") },
		"no build for this platform":           func(r *release, o *Options) { o.GOARCH = "riscv64" },
		"downgrade":                            func(r *release, o *Options) { o.Current = "2.0.0" },
		"redirect to another host":             func(r *release, o *Options) { r.redirectAssets = "https://evil.example.test/x" },
		"http endpoint": func(r *release, o *Options) {
			o.DownloadBase = strings.Replace(o.DownloadBase, "https://", "http://", 1)
		},
	}
	for name, mutate := range cases {
		copyArch := map[string][]byte{}
		for k2, v := range arch {
			copyArch[k2] = v
		}
		rel := newRelease(t, k, "v1.1.0", copyArch)
		exe := exeFile(t, "OLD BINARY")
		o := rel.opts(t, k, exe, "1.0.0")
		mutate(rel, &o)
		if _, err := Run(t.Context(), o); err == nil {
			t.Errorf("%s: the update must be refused", name)
		}
		if read(t, exe) != "OLD BINARY" {
			t.Errorf("%s: the installed binary changed", name)
		}
	}
}

func TestNoKeyMeansNoUpdate(t *testing.T) {
	if _, err := Run(t.Context(), Options{Current: "1.0.0"}); err != ErrNoKey {
		t.Fatalf("%v", err)
	}
}

func TestVersionComparison(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"v1.2.0", "1.1.9", 1}, {"1.0.0", "v1.0.0", 0}, {"v1.0.0-rc1", "1.0.0", -1}, {"2.0.0", "10.0.0", -1}, {"junk", "1.0.0", -1}, {"1.0.0", "junk", 1}} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

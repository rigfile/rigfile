package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUIShowsThePlanStoresDeclaredSecretsAndApplies(t *testing.T) {
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	rig := plainRig(t, "mcp_servers:\n  svc:\n    command: npx\n    args: [\"-y\", \"svc-mcp@1.0.0\"]\n    env:\n      SVC_KEY: secret://svc/key\nsecrets:\n  svc/key: {description: the service key}\n")

	urls := make(chan string, 1)
	var out, errb bytes.Buffer
	codes := make(chan int, 1)
	go func() {
		codes <- runWith(m, &out, &errb, func(e *env) { e.openURL = func(u string) error { urls <- u; return nil } }, "ui", rig, "--no-git")
	}()
	var landing string
	select {
	case landing = <-urls:
	case <-time.After(20 * time.Second):
		t.Fatalf("the UI did not start: %s %s", out.String(), errb.String())
	}
	u, _ := url.Parse(landing)
	jar, _ := cookiejar.New(nil)
	b := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) (int, string) {
		resp, err := b.Get("http://" + u.Host + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if resp, _ := b.Get(landing); resp.StatusCode != 303 {
		t.Fatalf("%d", resp.StatusCode)
	}
	code, page := get("/")
	for _, want := range []string{"Rig: jiaxu/plain", "Secret: svc/key", "not set yet", "commands/hi.md"} {
		if code != 200 || !strings.Contains(page, want) {
			t.Fatalf("missing %q:\n%s", want, page)
		}
	}
	tok := page[strings.Index(page, `name="csrf" value="`)+len(`name="csrf" value="`):]
	tok = tok[:strings.Index(tok, `"`)]
	post := func(path string, form url.Values) (int, string) {
		form.Set("csrf", tok)
		resp, err := b.PostForm("http://"+u.Host+path, form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	// nothing was written by looking
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "commands", "hi.md")); !os.IsNotExist(err) {
		t.Fatal("viewing the page changed the machine")
	}
	// a secret the rig does not declare is refused; the declared one is stored
	value := "FAKE-" + "UI-CMD-" + "5d3b9a10"
	if c, _ := post("/secret", url.Values{"ref": {"other/x"}, "value": {value}}); c != 400 {
		t.Fatalf("%d", c)
	}
	if c, _ := post("/secret", url.Values{"ref": {"svc/key"}, "value": {value}}); c != 303 {
		t.Fatalf("%d", c)
	}
	if r := m.run("", "secrets", "status", "svc/key"); !strings.Contains(r.out, "set") || strings.Contains(r.out+r.err, value) {
		t.Fatalf("%+v", r)
	}
	_, page = get("/")
	if !strings.Contains(page, "already stored") || strings.Contains(page, value) {
		t.Fatalf("%s", page)
	}
	// apply after confirming
	if c, body := post("/apply", url.Values{"confirm": {"yes"}}); c != 200 || !strings.Contains(body, "applied") {
		t.Fatalf("%d %s", c, body)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "commands", "hi.md")); err != nil {
		t.Fatal("the rig was not applied")
	}
	post("/quit", url.Values{})
	select {
	case c := <-codes:
		if c != 0 {
			t.Fatalf("exit %d: %s %s", c, out.String(), errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the UI did not stop")
	}
	if strings.Contains(out.String()+errb.String(), value) {
		t.Fatal("the secret value reached the terminal output")
	}
}

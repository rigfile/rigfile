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

	"github.com/rigfile/rigfile/internal/platform"
)

func TestUIShowsThePlanStoresDeclaredSecretsAndApplies(t *testing.T) {
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := platform.WritePrivate(pass, []byte("correct horse battery staple\n")); err != nil {
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
		if code != 200 || !strings.Contains(portable(page), want) { // plan paths use the OS separator
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

// TestUIApplyWithRefusedConflictsIsShownAsSuccess is a regression test for a real report (2026-09-27, the same
// session as the null-Origin fix): a rig applied cleanly except for an MCP server that already existed and was
// not managed by Rigfile, correctly left untouched. `rigfile apply` exits 3 for that ("applied but some items
// were refused" — main.go's own documented exit codes), which the UI used to treat as an outright failure
// ("That did not work"), reading as an error when nothing was wrong: the tool protected something it did not
// own, exactly as designed.
func TestUIApplyWithRefusedConflictsIsShownAsSuccess(t *testing.T) {
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := platform.WritePrivate(pass, []byte("correct horse battery staple\n")); err != nil {
		t.Fatal(err)
	}
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	rig := plainRig(t, "mcp_servers:\n  svc:\n    command: npx\n    args: [\"-y\", \"svc-mcp@1.0.0\"]\n")
	// a server by this name already exists and was not created by Rigfile: apply must refuse it, not overwrite it
	m.mcp.servers["svc"] = `{"command":"someone-elses-launcher"}`

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
	b.Get(landing)
	resp, _ := b.Get("http://" + u.Host + "/")
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	tok := string(page[strings.Index(string(page), `name="csrf" value="`)+len(`name="csrf" value="`):])
	tok = tok[:strings.Index(tok, `"`)]

	form := url.Values{"confirm": {"yes"}, "csrf": {tok}}
	resp, err := b.PostForm("http://"+u.Host+"/apply", form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	text := string(body)
	if resp.StatusCode != 200 {
		t.Fatalf("a refused conflict is not a server error: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(text, "That did not work") || strings.Contains(text, `class="bad"`) {
		t.Fatalf("a safe, protective refusal must not be shown as a failure:\n%s", text)
	}
	if !strings.Contains(text, "<h1>Done</h1>") {
		t.Fatalf("missing the normal success heading:\n%s", text)
	}
	if !strings.Contains(text, "not managed by Rigfile") || !strings.Contains(text, "1 item(s) were refused") {
		t.Fatalf("the refusal itself must still be visible in the transcript:\n%s", text)
	}

	b.PostForm("http://"+u.Host+"/quit", url.Values{"csrf": {tok}})
	select {
	case c := <-codes:
		if c != 0 {
			t.Fatalf("exit %d: %s %s", c, out.String(), errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the UI did not stop")
	}
}

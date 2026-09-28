package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigfile/rigfile/internal/scan"
	"github.com/rigfile/rigfile/internal/scan/corpus"
)

const rigYAML = `apiVersion: rigfile.dev/v1
name: adams/demo
version: 1.0.0
description: A demo rig
instructions:
  - {id: style, file: instructions/style.md}
skills:
  - {path: skills/pdf}
mcp_servers:
  alpaca:
    command: npx
    args: ['-y', 'alpaca-mcp@1.4.2']
    env: {ALPACA_API_KEY: 'secret://alpaca/api_key', ALPACA_PAPER: 'true'}
secrets:
  alpaca/api_key: {description: Alpaca key, obtain_url: 'https://example.test/keys'}
`

func cleanFiles() map[string][]byte {
	return map[string][]byte{
		"rigfile.yaml":            []byte(rigYAML),
		"instructions/style.md":   []byte("# Style\n- be terse\n"),
		"skills/pdf/SKILL.md":     []byte("---\nname: pdf\ndescription: PDFs\n---\nbody\n"),
		"skills/pdf/scripts/x.sh": []byte("#!/bin/sh\necho hi\n"),
	}
}

func fakeToken() string { return "gh" + "p_" + "wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo" }

func prep(t *testing.T, files map[string][]byte, mut func(*Input)) *Prepared {
	t.Helper()
	in := Input{Files: files, Home: "/Users/ada", User: "ada", Host: "adas-mbp"}
	if mut != nil {
		mut(&in)
	}
	p, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestACleanRigPublishesWithReadmeAndScanProof(t *testing.T) {
	p := prep(t, cleanFiles(), nil)
	if b := p.Blocked(); len(b) > 0 {
		t.Fatalf("blocked: %v", b)
	}
	if p.Proof.Findings != 0 || p.Proof.Files < 5 {
		t.Fatalf("%+v", p.Proof)
	}
	rd := string(p.Files["README.md"])
	for _, want := range []string{"# adams/demo", "rigfile pull github.com/adams/demo", "rigfile pull adams/demo --registry " + RegistryURLPlaceholder, "alpaca-mcp@1.4.2", "alpaca/api_key", "https://example.test/keys", "base-secure"} {
		if !strings.Contains(rd, want) {
			t.Errorf("README missing %q:\n%s", want, rd)
		}
	}
	out := filepath.Join(t.TempDir(), "repo")
	if err := p.Write(out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"rigfile.yaml", "README.md", ".gitignore", "skills/pdf/scripts/x.sh"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Write(out); err == nil {
		t.Fatal("a non-empty output directory must be refused")
	}
}

func TestASecretBlocksPublishingAndTheValueIsNeverReported(t *testing.T) {
	files := cleanFiles()
	files["instructions/style.md"] = []byte("# Style\ntoken = \"" + fakeToken() + "\"\n")
	p := prep(t, files, func(in *Input) { in.AckPersonal = true })
	if len(p.Secrets) == 0 || len(p.Blocked()) == 0 {
		t.Fatalf("%+v", p)
	}
	for _, f := range p.Secrets {
		if strings.Contains(f.Sample+f.Rule+f.File, fakeToken()) {
			t.Fatal("the report leaked the value")
		}
	}
	out := filepath.Join(t.TempDir(), "repo")
	if err := p.Write(out); err == nil {
		t.Fatal("a blocked rig must not be written")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("nothing may be written for a blocked rig")
	}
	// a secret inside a skill script, and a credential file name, block too
	files = cleanFiles()
	files["skills/pdf/scripts/x.sh"] = []byte("#!/bin/sh\ncurl -H 'Authorization: Bearer " + fakeToken() + "' x\n")
	if p := prep(t, files, nil); len(p.Secrets) == 0 {
		t.Fatal("a secret in a script must block")
	}
	files = cleanFiles()
	files["skills/pdf/.env"] = []byte("A=1\n")
	if p := prep(t, files, nil); len(p.Secrets) == 0 {
		t.Fatal("a credential file name must block")
	}
}

func TestHomePathsAreRewrittenAndPersonalInformationNeedsAcknowledgement(t *testing.T) {
	files := cleanFiles()
	files["instructions/style.md"] = []byte("Projects live in /Users/ada/code/app and C:\\Users\\ada\\code, or /home/bob/x.\n" +
		"Ask ada@bytebuilderslab.app or +1 415 555 0134 (adas-mbp). Docs: user@example.com, git@github.com:o/r.git.\n")
	p := prep(t, files, nil)
	text := string(p.Files["instructions/style.md"])
	if strings.Contains(text, "/Users/ada") || strings.Contains(text, `C:\Users`) || strings.Contains(text, "/home/bob") || !strings.Contains(text, "~/code/app") {
		t.Fatalf("home paths not rewritten: %q", text)
	}
	if len(p.Rewritten) == 0 {
		t.Fatal("rewrites must be reported")
	}
	kinds := map[string]int{}
	for _, f := range p.Personal {
		kinds[f.Rule]++
		if strings.Contains(f.Sample, "gmail") || strings.Contains(f.Sample, "555") {
			t.Fatalf("sample leaks: %q", f.Sample)
		}
	}
	if kinds["email"] != 1 || kinds["phone"] != 1 || kinds["hostname"] != 1 {
		t.Fatalf("%v (placeholder e-mails and git@host must not count)", kinds)
	}
	if b := p.Blocked(); len(b) != 1 || !strings.Contains(b[0], "--ack-personal") {
		t.Fatalf("%v", b)
	}
	// acknowledged: publishes
	p = prep(t, files, func(in *Input) { in.AckPersonal = true })
	if b := p.Blocked(); len(b) != 0 {
		t.Fatalf("%v", b)
	}
	// the OS user name as a word is flagged, but not inside another word
	files = cleanFiles()
	files["instructions/style.md"] = []byte("hello ada\nAdams and adamo are other words\n")
	p = prep(t, files, nil)
	n := 0
	for _, f := range p.Personal {
		if f.Rule == "username" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("username findings = %d", n)
	}
}

func TestAnInvalidRigIsRejected(t *testing.T) {
	files := cleanFiles()
	delete(files, "skills/pdf/SKILL.md")
	delete(files, "skills/pdf/scripts/x.sh")
	p, err := Prepare(Input{Files: files, Home: "/Users/ada", AckPersonal: true})
	if err != nil || len(p.Blocked()) == 0 {
		t.Fatalf("a rig that references a missing skill must not publish: %v", err)
	}
	files = cleanFiles()
	files["rigfile.yaml"] = []byte(strings.Replace(rigYAML, "adams/demo", "rigfile/evil", 1))
	if _, err := Prepare(Input{Files: files}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("%v", err)
	}
	if _, err := Prepare(Input{Files: map[string][]byte{"README.md": []byte("x")}}); err == nil {
		t.Fatal("no manifest")
	}
}

func TestFromDirCopiesOnlyWhatTheManifestReferencesAndRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	for f, c := range cleanFiles() {
		p := filepath.Join(dir, filepath.FromSlash(f))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, c, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "notes"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "notes", "private.md"), []byte("my diary"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1"), 0o644)
	files, err := FromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["notes/private.md"]; ok {
		t.Fatal("an unreferenced file was copied")
	}
	if _, ok := files[".env"]; ok {
		t.Fatal(".env was copied")
	}
	for _, f := range []string{"rigfile.yaml", "instructions/style.md", "skills/pdf/SKILL.md", "skills/pdf/scripts/x.sh"} {
		if _, ok := files[f]; !ok {
			t.Fatalf("missing %s: %v", f, keys(files))
		}
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "skills", "pdf", "link")); err == nil {
		if _, err := FromDir(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("%v", err)
		}
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Every core positive of the scanner corpus, placed inside a skill of an otherwise clean rig, blocks publishing.
func TestEveryCorpusSecretBlocksPublishing(t *testing.T) {
	sc, err := scan.New(scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	scf := func() (*scan.Scanner, error) { return sc, nil }
	var missed []string
	n := 0
	for _, s := range corpus.Positives(20260925) {
		if s.Tier != "core" {
			continue
		}
		n++
		files := cleanFiles()
		files["skills/pdf/"+strings.TrimPrefix(s.Path, "/")] = []byte(s.Content)
		p, err := Prepare(Input{Files: files, Home: "/Users/ada", AckPersonal: true, Scanner: scf})
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		if len(p.Secrets) == 0 || len(p.Blocked()) == 0 {
			missed = append(missed, s.ID)
		}
	}
	if n < 100 || len(missed) > 0 {
		t.Fatalf("%d core positives, %d not blocked: %v", n, len(missed), missed)
	}
}

func TestUnpinnedPackagesBlockPublishing(t *testing.T) {
	files := cleanFiles()
	files["rigfile.yaml"] = []byte(strings.Replace(rigYAML, "alpaca-mcp@1.4.2", "alpaca-mcp", 1))
	p := prep(t, files, func(in *Input) { in.AckPersonal = true })
	b := p.Blocked()
	if len(b) != 1 || !strings.Contains(b[0], "not pinned") {
		t.Fatalf("%v", b)
	}
}

func TestGenericAccountNamesAreNotPersonalInformation(t *testing.T) {
	p := prep(t, cleanFiles(), func(in *Input) { in.User, in.Host = "dev", "runner" }) // rigfile.yaml says apiVersion: rigfile.dev/v1
	for _, f := range p.Personal {
		t.Errorf("unexpected: %+v", f)
	}
}

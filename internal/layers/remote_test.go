package layers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

type fakeRemote struct {
	dirs map[string]string // spec -> rig dir
	seen []string
}

func (f *fakeRemote) ResolveRemote(spec string) (*manifest.Loaded, RemoteInfo, error) {
	f.seen = append(f.seen, spec)
	d, ok := f.dirs[spec]
	if !ok {
		return nil, RemoteInfo{}, fmt.Errorf("no such source")
	}
	l, err := manifest.Load(d)
	return l, RemoteInfo{Source: spec, Commit: strings.Repeat("c", 40), TreeSHA256: strings.Repeat("d", 64)}, err
}

func rigWithFrom(t *testing.T, name, from string) string {
	t.Helper()
	d := t.TempDir()
	body := "apiVersion: rigfile.dev/v1\nname: " + name + "\nversion: 1.0.0\n"
	if from != "" {
		body += "from: [" + from + "]\n"
	}
	if err := os.WriteFile(filepath.Join(d, "rigfile.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRemoteLayerResolvesAndIsRecorded(t *testing.T) {
	remote := rigWithFrom(t, "friend/base", "")
	top, err := manifest.Load(rigWithFrom(t, "me/top", "github.com/friend/base@v1"))
	if err != nil {
		t.Fatal(err)
	}
	if problems := manifest.Check(top); manifest.HasErrors(problems) {
		t.Fatalf("a git source must be a valid `from:` entry: %v", problems)
	}
	fr := &fakeRemote{dirs: map[string]string{"github.com/friend/base@v1": remote}}
	res, err := Resolve(top, WithRemote(DirSource{}, fr))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Layers) != 2 || res.Layers[0].Name != "friend/base" || res.Remotes["friend/base"].Commit == "" {
		t.Fatalf("%+v %+v", res.Layers, res.Remotes)
	}
}

func TestRemoteLayerCannotWeakenBase(t *testing.T) {
	imposter := rigWithFrom(t, "rigfile/base-secure", "")
	top, _ := manifest.Load(rigWithFrom(t, "me/top", "github.com/evil/base"))
	fr := &fakeRemote{dirs: map[string]string{"github.com/evil/base": imposter}}
	if _, err := Resolve(top, WithRemote(DirSource{}, fr)); err == nil || !strings.Contains(err.Error(), "reserved name") {
		t.Fatalf("%v", err)
	}
}

func TestGitSourceWithoutARemoteResolverIsAClearError(t *testing.T) {
	top, _ := manifest.Load(rigWithFrom(t, "me/top", "github.com/friend/base"))
	if _, err := Resolve(top, DirSource{}); err == nil || !strings.Contains(err.Error(), "git source") {
		t.Fatalf("%v", err)
	}
}

package layers

import (
	"context"
	"fmt"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

// SourceRemote resolves git-source layers through a source.Client, holding each one to the pin an earlier lock
// recorded (so a moved tag or changed content is refused, not silently re-resolved).
type SourceRemote struct {
	Client *source.Client
	Pins   map[string]source.Pin // canonical source string -> pin
	Ctx    context.Context
	// Registry is the origin of the configured Rigfile registry ("" = none): owner/name layers that are not in the
	// layers directory are looked up there.
	Registry string
}

// ResolveRegistry implements RegistryResolver.
func (s SourceRemote) ResolveRegistry(ref Ref) (*manifest.Loaded, RemoteInfo, error) {
	if s.Registry == "" {
		return nil, RemoteInfo{}, &NotFoundError{ref}
	}
	spec := source.Spec{Kind: source.Registry, URL: s.Registry, Path: ref.Name, Ref: ref.Range}
	var pin *source.Pin
	if p, ok := s.Pins[spec.String()]; ok {
		pin = &p
	}
	ctx := s.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := s.Client.Get(ctx, spec, pin)
	if err != nil {
		return nil, RemoteInfo{}, err
	}
	l, err := manifest.Load(f.Dir)
	if err != nil {
		return nil, RemoteInfo{}, fmt.Errorf("%s: %w", spec, err)
	}
	if l.M.Name != ref.Name {
		return nil, RemoteInfo{}, fmt.Errorf("layers: the registry served %q for %q", l.M.Name, ref.Name)
	}
	return l, RemoteInfo{Source: spec.String(), Commit: f.Commit, TreeSHA256: f.TreeSHA256}, nil
}

// ResolveRemote implements Remote.
func (s SourceRemote) ResolveRemote(raw string) (*manifest.Loaded, RemoteInfo, error) {
	spec, err := source.Parse(raw)
	if err != nil {
		return nil, RemoteInfo{}, err
	}
	var pin *source.Pin
	if p, ok := s.Pins[spec.String()]; ok {
		pin = &p
	}
	ctx := s.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := s.Client.Get(ctx, spec, pin)
	if err != nil {
		return nil, RemoteInfo{}, err
	}
	l, err := manifest.Load(f.Dir)
	if err != nil {
		return nil, RemoteInfo{}, fmt.Errorf("%s: %w", spec, err)
	}
	return l, RemoteInfo{Source: spec.String(), Commit: f.Commit, TreeSHA256: f.TreeSHA256}, nil
}

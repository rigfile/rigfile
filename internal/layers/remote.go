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

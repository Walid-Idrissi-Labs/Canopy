package mcp

// Connecting to several servers, where some of them will not work.
//
// "A failing server degrades that server only" is half of A8-06's acceptance and it is the half
// that is easy to get wrong by writing the obvious loop, because the obvious loop returns on the
// first error. Every server here is dialled on its own and reported on its own.

import (
	"context"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Failure is a server that could not be used, and why.
//
// Kept and returned rather than logged, because a server silently contributing nothing is
// indistinguishable from a server that was never configured, and the user needs to be able to tell
// those apart without reading a log file.
type Failure struct {
	Server string
	Err    error
}

// Set is the result of connecting to every configured server.
type Set struct {
	Sessions []*Session
	Failures []Failure
}

// Tools returns every tool from every server that worked.
func (s *Set) Tools() []core.Tool {
	var out []core.Tool
	for _, session := range s.Sessions {
		out = append(out, session.Tools()...)
	}
	return out
}

// Close stops every server.
//
// Concurrently, for the same reason ConnectAll dials concurrently and one more besides: each Close
// gives its server a fixed moment to leave on its own before signalling the group it leads, and in
// series that moment is paid once per configured server while somebody waits to get their terminal
// back.
func (s *Set) Close() {
	var wg sync.WaitGroup
	for _, session := range s.Sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session.Close()
		}()
	}
	wg.Wait()
}

// ConnectAll dials every enabled server.
//
// Concurrently, because starting six servers one after another means waiting for six process
// startups in series and the slow one is usually a package manager fetching something. The results
// are put back in configuration order afterwards, so what the user sees matches what they wrote
// rather than which server happened to win the race.
func ConnectAll(ctx context.Context, specs []Spec) *Set {
	sessions := make([]*Session, len(specs))
	failures := make([]*Failure, len(specs))

	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := Connect(ctx, spec)
			if err != nil {
				failures[i] = &Failure{Server: spec.Name, Err: err}
				return
			}
			sessions[i] = session
		}()
	}
	wg.Wait()

	set := &Set{}
	for i := range specs {
		if sessions[i] != nil {
			set.Sessions = append(set.Sessions, sessions[i])
		}
		if failures[i] != nil {
			set.Failures = append(set.Failures, *failures[i])
		}
	}
	return set
}

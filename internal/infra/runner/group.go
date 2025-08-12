package runner

import (
	"context"
	"sync"
)

type Group struct {
	wg  sync.WaitGroup
}

func (g *Group) Go(ctx context.Context, fn func(ctx context.Context) error) <-chan error {
	done := make(chan error, 1)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		done <- fn(ctx)
		close(done)
	}()
	return done
}

func (g *Group) Wait() { g.wg.Wait() }

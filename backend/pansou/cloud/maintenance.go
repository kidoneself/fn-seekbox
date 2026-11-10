package cloud

import (
	"context"
	"errors"
	"sync"
)

var ErrMaintenance = errors.New("应用正在准备更新，请稍后重试")

// The admission lock closes before draining, so a newly queued job, another
// fnOS user, the scheduler or an HTTP request cannot slip past a busy check.
type maintenanceGate struct {
	mu      sync.Mutex
	paused  bool
	active  int
	changed chan struct{}
}

func (g *maintenanceGate) signalLocked() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}
func (s *Service) beginOperation() (func(), error) {
	g := &s.maintenance
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		return nil, ErrMaintenance
	}
	g.active++
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); g.active--; g.signalLocked(); g.mu.Unlock() }) }, nil
}

// PrepareUpdate closes admission for the whole service, including every owner.
// Queued jobs remain durable. Already admitted transfers and QR sessions finish.
// A canceled drain reopens admission; successful preparation requires ResumeUpdate
// unless the update helper replaces this process.
func (s *Service) PrepareUpdate(ctx context.Context) error {
	g := &s.maintenance
	g.mu.Lock()
	if g.paused {
		g.mu.Unlock()
		return ErrMaintenance
	}
	g.paused = true
	g.signalLocked()
	for g.active != 0 {
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			s.ResumeUpdate()
			return ctx.Err()
		case <-changed:
		}
		g.mu.Lock()
	}
	g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.ResumeUpdate()
		return err
	}
	return nil
}
func (s *Service) ResumeUpdate() {
	g := &s.maintenance
	g.mu.Lock()
	g.paused = false
	g.signalLocked()
	g.mu.Unlock()
	s.notify()
}
func (s *Service) UpdateMaintenance() (bool, int) {
	g := &s.maintenance
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused, g.active
}

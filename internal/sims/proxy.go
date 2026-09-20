package sims

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
)

// captures holds the running capture per device, so a second start on the same device replaces the
// first instead of leaving an orphan that still owns the machine's proxy settings.
type captures struct {
	mu sync.Mutex
	by map[string]*capture.Session
}

func (c *captures) get(id string) (*capture.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.by[id]
	return s, ok
}

func (c *captures) put(id string, s *capture.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.by == nil {
		c.by = map[string]*capture.Session{}
	}
	c.by[id] = s
}

func (c *captures) take(id string) (*capture.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.by[id]
	delete(c.by, id)
	return s, ok
}

func (c *captures) all() []*capture.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*capture.Session, 0, len(c.by))
	for _, s := range c.by {
		out = append(out, s)
	}
	return out
}

// CanLog reports whether a log stream can be opened for a device, and for one app in particular
// when app is given. A front end asks before opening the view, since the alternative is a screen
// that appears and then says it cannot do the thing it was opened for.
func (m *Manager) CanLog(ctx context.Context, d device.Device, app *device.App) error {
	cmd, err := m.LogCmd(ctx, d, app)
	if err != nil {
		return err
	}
	if cmd == nil {
		return fmt.Errorf("%s has no log stream sims can follow", d.Name)
	}
	// the command was only built, never started; releasing it costs nothing
	return nil
}

// CanCapture reports whether a device can be pointed at the proxy.
func (m *Manager) CanCapture(d device.Device) bool {
	p, err := m.provider(d)
	if err != nil {
		return false
	}
	_, ok := p.(device.Proxier)
	return ok
}

// StartCapture points d at a proxy of its own and returns the running session. Starting a capture on
// a device that already has one stops the old one first.
func (m *Manager) StartCapture(ctx context.Context, d device.Device, o capture.Options) (*capture.Session, error) {
	p, err := m.provider(d)
	if err != nil {
		return nil, err
	}
	if !d.Reachable() {
		return nil, fmt.Errorf("%s is %s; a capture needs a device that can answer", d.Name, d.State)
	}
	if old, ok := m.captures.take(d.ID); ok {
		_ = old.Stop()
	}
	s, err := capture.Start(ctx, p, d, o)
	if err != nil {
		return nil, err
	}
	m.captures.put(d.ID, s)
	return s, nil
}

// Capture returns the running capture for a device.
func (m *Manager) Capture(d device.Device) (*capture.Session, bool) {
	return m.captures.get(d.ID)
}

// Captures returns every running capture.
func (m *Manager) Captures() []*capture.Session { return m.captures.all() }

// StopCapture ends the capture on a device and puts its settings back.
func (m *Manager) StopCapture(d device.Device) error {
	s, ok := m.captures.take(d.ID)
	if !ok {
		return fmt.Errorf("no capture is running on %s", d.Name)
	}
	return s.Stop()
}

// Leftover reports a capture that was killed before it could put this machine and its device back.
func (m *Manager) Leftover() capture.Leftover { return capture.FindLeftover("") }

// CleanLeftover undoes it, clearing the device through whichever provider owns it.
func (m *Manager) CleanLeftover(ctx context.Context, l capture.Leftover) error {
	return l.Clean(ctx, func(ctx context.Context, d device.Device) error {
		p, err := m.provider(d)
		if err != nil {
			return err
		}
		proxier, ok := p.(device.Proxier)
		if !ok {
			return fmt.Errorf("%s cannot clear a proxy from here", d.Platform)
		}
		return proxier.ClearProxy(ctx, d)
	})
}

// StopAllCaptures is what a front end calls on its way out: a device or a Mac left pointing at a
// proxy that has stopped cannot reach the network, so this must run even on an unclean exit.
func (m *Manager) StopAllCaptures() error {
	var errs []error
	m.captures.mu.Lock()
	sessions := make([]*capture.Session, 0, len(m.captures.by))
	for id, s := range m.captures.by {
		sessions = append(sessions, s)
		delete(m.captures.by, id)
	}
	m.captures.mu.Unlock()
	for _, s := range sessions {
		if err := s.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

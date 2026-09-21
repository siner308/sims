package sims

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// A phone's profile keeps sending its traffic here after a capture ends, because only the phone's owner can remove it.
// A standby is a listener on that port that relays everything untouched, so the phone keeps its network while sims is open and no capture is running.
// A capture on the phone takes the port over, and the standby comes back when it stops.
type standby struct {
	srv *proxy.Server
	rec capture.PhoneRecord
	dir string
}

type standbys struct {
	mu sync.Mutex
	by map[string]*standby
	// paused holds the standby a capture displaced, so StopCapture can put it back without reading the records again
	paused map[string]*standby
}

// StartStandbys relays for every phone that was given a profile and has no capture running. It
// returns the phones now relayed; an error names the ones whose port could not be taken.
func (m *Manager) StartStandbys(ctx context.Context) ([]capture.PhoneRecord, error) {
	dir, err := capture.DefaultCertDir()
	if err != nil {
		return nil, err
	}
	var relayed []capture.PhoneRecord
	var errs []error
	for _, rec := range capture.PhoneRecords(dir) {
		if _, running := m.captures.get(rec.Device.ID); running {
			continue
		}
		if err := m.startStandby(dir, rec); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rec.Device.Name, err))
			continue
		}
		relayed = append(relayed, rec)
	}
	return relayed, errors.Join(errs...)
}

// StartStandby relays one phone's traffic untouched until StopStandby or StopAllCaptures.
func (m *Manager) StartStandby(ctx context.Context, d device.Device) (capture.PhoneRecord, error) {
	dir, err := capture.DefaultCertDir()
	if err != nil {
		return capture.PhoneRecord{}, err
	}
	rec, ok := capture.Phone(dir, d.ID)
	if !ok {
		return rec, fmt.Errorf("%s has no profile from sims yet; a capture installs one", d.Name)
	}
	return rec, m.startStandby(dir, rec)
}

func (m *Manager) StopStandby(d device.Device) {
	m.standbys.mu.Lock()
	sb := m.standbys.by[d.ID]
	delete(m.standbys.by, d.ID)
	m.standbys.mu.Unlock()
	if sb != nil {
		_ = sb.srv.Close()
	}
}

// Relaying reports whether a phone's traffic is being passed through untouched right now.
func (m *Manager) Relaying(d device.Device) bool {
	m.standbys.mu.Lock()
	defer m.standbys.mu.Unlock()
	_, ok := m.standbys.by[d.ID]
	return ok
}

// PhoneRecord is what an earlier capture gave the phone, if anything.
func (m *Manager) PhoneRecord(d device.Device) (capture.PhoneRecord, bool) {
	dir, err := capture.DefaultCertDir()
	if err != nil {
		return capture.PhoneRecord{}, false
	}
	return capture.Phone(dir, d.ID)
}

func (m *Manager) startStandby(dir string, rec capture.PhoneRecord) error {
	m.standbys.mu.Lock()
	defer m.standbys.mu.Unlock()
	if m.standbys.by == nil {
		m.standbys.by = map[string]*standby{}
	}
	if _, ok := m.standbys.by[rec.Device.ID]; ok {
		return nil
	}
	ca, err := proxy.LoadOrCreateCA(dir)
	if err != nil {
		return err
	}
	srv := &proxy.Server{
		CA:        ca,
		Store:     proxy.NewStore(0),
		Attribute: func(context.Context, string) proxy.Attribution { return proxy.Attribution{Ignore: true} },
	}
	if _, err := srv.Listen(fmt.Sprintf("0.0.0.0:%d", rec.Port)); err != nil {
		return fmt.Errorf("port %d, where the phone's profile sends its traffic, is taken: %w", rec.Port, err)
	}
	go srv.Serve()
	m.standbys.by[rec.Device.ID] = &standby{srv: srv, rec: rec, dir: dir}
	return nil
}

func (m *Manager) pauseStandby(id string) {
	m.standbys.mu.Lock()
	sb := m.standbys.by[id]
	delete(m.standbys.by, id)
	if sb != nil {
		if m.standbys.paused == nil {
			m.standbys.paused = map[string]*standby{}
		}
		m.standbys.paused[id] = sb
	}
	m.standbys.mu.Unlock()
	if sb != nil {
		_ = sb.srv.Close()
	}
}

func (m *Manager) resumeStandby(id string) {
	m.standbys.mu.Lock()
	sb := m.standbys.paused[id]
	delete(m.standbys.paused, id)
	m.standbys.mu.Unlock()
	if sb != nil {
		_ = m.startStandby(sb.dir, sb.rec)
	}
}

func (m *Manager) closeStandbys() {
	m.standbys.mu.Lock()
	all := m.standbys.by
	m.standbys.by = nil
	m.standbys.paused = nil
	m.standbys.mu.Unlock()
	for _, sb := range all {
		_ = sb.srv.Close()
	}
}

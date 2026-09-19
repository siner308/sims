package capture

import (
	"context"
	"testing"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// SessionForTest builds a session with a given scope without starting a proxy or touching a device,
// so the scope rules can be tested on their own.
func SessionForTest(t *testing.T, d device.Device, scope Scope) *Session {
	t.Helper()
	return &Session{Device: d, scope: scope, host: &hostProxy{}}
}

// AttributeForTest exposes the scope decision for one client address.
func (s *Session) AttributeForTest(ctx context.Context, clientAddr string) proxy.Attribution {
	return s.attribute(ctx, clientAddr)
}

// ListenAddrForTest is the address the proxy actually bound.
func (s *Session) ListenAddrForTest() string {
	if s.srv == nil || s.srv.Addr() == nil {
		return ""
	}
	return s.srv.Addr().String()
}

// WriteDeadJournalForTest leaves the journal a killed capture would have left, with a pid that is
// certain to be gone.
func WriteDeadJournalForTest(t *testing.T, dir string, d device.Device) {
	t.Helper()
	j := journal{
		PID:  -1, // never a live process
		Port: 65123,
		Device: &journalDevice{
			ID: d.ID, Name: d.Name, Serial: d.Serial,
			Platform: string(d.Platform), Kind: string(d.Kind),
		},
	}
	if err := writeJournal(dir, j); err != nil {
		t.Fatal(err)
	}
}

// SimulatorProcessNameForTest exposes the naming used for a simulator's connections.
func SimulatorProcessNameForTest(p proxy.Process) string { return simulatorProcessName(p) }

// NeedsHostProxyForTest exposes which devices borrow this machine's network settings.
func NeedsHostProxyForTest(d device.Device) bool { return needsHostProxy(d) }

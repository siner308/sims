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

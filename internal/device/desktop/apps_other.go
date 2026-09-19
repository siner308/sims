//go:build !darwin

package desktop

import (
	"context"

	"github.com/siner308/sims/internal/device"
)

// Apps is not implemented away from macOS yet; the list is empty rather than an error, so the view
// opens and says there is nothing to show.
func (p *Provider) Apps(context.Context, device.Device) ([]device.App, error) {
	return nil, nil
}

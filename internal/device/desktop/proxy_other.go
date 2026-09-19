//go:build !darwin && !windows

package desktop

import (
	"context"
	"errors"

	"github.com/siner308/sims/internal/device"
)

func (p *Provider) SetProxy(context.Context, device.Device, device.ProxyTarget) ([]device.ProxyStep, error) {
	return nil, errors.New("sims cannot capture this machine's own traffic yet")
}

func (p *Provider) ClearProxy(context.Context, device.Device) error { return nil }

func (p *Provider) ProxyState(context.Context, device.Device) (device.ProxyState, error) {
	return device.ProxyState{}, nil
}

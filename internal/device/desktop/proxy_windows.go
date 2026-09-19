package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/siner308/sims/internal/device"
)

// SetProxy trusts the capture's certificate for the current user. WinINET's proxy setting, which is
// what most Windows apps follow, is the machine's and belongs to the capture.
func (p *Provider) SetProxy(ctx context.Context, _ device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	steps := []device.ProxyStep{
		{Title: "proxy", Detail: "this machine's proxy points at " + t.Addr() + " while the capture runs"},
	}
	if len(t.CACert) == 0 {
		return steps, nil
	}
	f, err := os.CreateTemp("", "sims-ca-*.cer")
	if err != nil {
		return steps, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(t.CACert); err != nil {
		f.Close()
		return steps, err
	}
	if err := f.Close(); err != nil {
		return steps, err
	}
	// CurrentUser\Root needs no administrator rights, unlike the machine store
	cmd := exec.CommandContext(ctx, "certutil", "-user", "-addstore", "-f", "Root", f.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return append(steps, device.ProxyStep{
			Title:  "certificate",
			Detail: fmt.Sprintf("could not be added to your Root store (%v); run: certutil -user -addstore -f Root <cert>", err),
			Manual: true,
		}), nil
	} else {
		_ = out
	}
	return append(steps, device.ProxyStep{
		Title:  "certificate",
		Detail: t.CertName + " is trusted for your user; an app with its own trust store (Firefox) needs it separately",
	}), nil
}

func (p *Provider) ClearProxy(context.Context, device.Device) error { return nil }

func (p *Provider) ProxyState(context.Context, device.Device) (device.ProxyState, error) {
	return device.ProxyState{}, nil
}

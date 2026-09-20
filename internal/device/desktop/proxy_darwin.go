package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/siner308/sims/internal/device"
)

// SetProxy reports what a capture of this machine needs. The proxy setting itself is the machine's,
// which the capture owns and restores.
//
// Trusting the certificate is left to the user on purpose: `security add-trusted-cert` puts up a
// GUI authorisation panel and waits for it indefinitely, which inside the TUI means sims hangs with
// no way to answer. The command is printed instead, and `sims proxy ca --install localhost` runs it
// from a shell where the panel can actually be answered.
func (p *Provider) SetProxy(_ context.Context, _ device.Device, t device.ProxyTarget) ([]device.ProxyStep, error) {
	steps := []device.ProxyStep{
		{Title: "proxy", Detail: "this Mac's web proxy points at " + t.Addr() + " while the capture runs"},
	}
	if len(t.CACert) == 0 {
		return steps, nil
	}
	if trustedHere(t.CACert) {
		return append(steps, device.ProxyStep{
			Title:  "certificate",
			Detail: t.CertName + " is already trusted on this Mac",
		}), nil
	}
	return append(steps, device.ProxyStep{
		Title: "certificate",
		Detail: "HTTPS stays unopened until this Mac trusts it. In a terminal:\n" +
			"      sims proxy ca " + ID + " --install   (macOS will ask for your password)",
		Manual: true,
	}), nil
}

// TrustCert adds the root to the user's login keychain. It is called from the command line, never
// from the TUI, because macOS puts up an authorisation panel that has to be answered.
func (p *Provider) TrustCert(ctx context.Context, certPEM []byte) error {
	f, err := os.CreateTemp("", "sims-ca-*.pem")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(certPEM); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "security", "add-trusted-cert",
		"-k", home+"/Library/Keychains/login.keychain-db", f.Name())
	// the authorisation panel is the user's to answer, so the command keeps the terminal
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("security add-trusted-cert: %w", err)
	}
	return nil
}

// trustedHere reports whether this Mac will actually trust the certificate, which is a different
// question from whether it is in a keychain: a certificate whose trust was revoked in Keychain
// Access is still there. Answering the easy question would suppress the install instruction and
// leave every HTTPS exchange unopened with nothing on screen saying why.
//
// x509.SystemCertPool cannot answer it either, since Go hands verification to the platform and the
// pool comes back empty on macOS. `security verify-cert` is the platform asking itself.
func trustedHere(certPEM []byte) bool {
	f, err := os.CreateTemp("", "sims-trust-*.pem")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(certPEM); err != nil {
		f.Close()
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// the basic policy asks whether the chain is trusted; the ssl policy adds a certificate
	// transparency check that a private CA can never satisfy
	return exec.CommandContext(ctx, "security", "verify-cert", "-c", f.Name(), "-p", "basic").Run() == nil
}

// ClearProxy leaves the certificate in place, the way the simulator path does: a capture the user
// runs again should not ask for the same approval twice.
func (p *Provider) ClearProxy(context.Context, device.Device) error { return nil }

func (p *Provider) ProxyState(context.Context, device.Device) (device.ProxyState, error) {
	return device.ProxyState{}, nil
}

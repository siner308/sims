package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

func (c *cli) proxyCmd() *cobra.Command {
	cmd := group("proxy", "Watch a device's HTTP traffic", "traffic")
	cmd.AddCommand(c.proxyRunCmd(), c.proxyCACmd(), c.proxyCleanCmd(), c.proxyStandbyCmd())
	return cmd
}

func (c *cli) proxyStandbyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "standby <phone>",
		Short: "Relay an iPhone's traffic untouched, so a phone whose profile points here keeps its network",
		Long: "An iPhone's profile keeps sending its traffic to this Mac after a capture ends, and only the phone's owner can remove it.\n" +
			"This listens on that port and passes everything through unread until interrupted. The TUI does the same while it is open.",
		Args: cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			rec, err := c.Manager.StartStandby(ctx, d)
			if err != nil {
				return err
			}
			defer c.Manager.StopStandby(d)
			fmt.Fprintf(c.Err, "relaying %s untouched on port %d; press ctrl+c to stop\n", d.Name, rec.Port)
			ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer cancel()
			<-ctx.Done()
			return nil
		}),
	}
	return cmd
}

// asks before opening Settings: Safari fetches the profile before its Allow prompt is answered, so opening Settings on the fetch alone would cover that prompt
func (c *cli) settingsAfterFetch(ctx context.Context, d device.Device, fetched <-chan struct{}) {
	select {
	case <-fetched:
	case <-ctx.Done():
		return
	}
	fmt.Fprintln(c.Err, "   Safari on the phone has the profile")
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	fmt.Fprintln(c.Err, "   once you have tapped Allow, press enter here and sims opens Settings on the phone")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		return
	}
	if err := c.Manager.OpenSettings(ctx, d); err != nil {
		fmt.Fprintln(c.Err, "sims:", err)
		return
	}
	fmt.Fprintln(c.Err, "   Settings is open on the phone: tap Profile Downloaded at the top, then Install")
}

func printSteps(w io.Writer, steps []device.ProxyStep) {
	for _, s := range steps {
		mark := " "
		if s.Manual {
			mark = "!"
		}
		fmt.Fprintf(w, " %s %-12s %s\n", mark, s.Title, s.Detail)
		for i, tap := range s.Todo {
			fmt.Fprintf(w, "%16s%d. %s\n", "", i+1, tap)
		}
	}
}

func (c *cli) proxyRunCmd() *cobra.Command {
	var (
		port     int
		harPath  string
		quiet    bool
		duration time.Duration
		maxBody  int
		all      bool
		ssid     string
		setup    bool
	)
	cmd := &cobra.Command{
		Use:   "run <device>",
		Short: "Point a device at a proxy and print its traffic until interrupted",
		Long: "Point a device at a local proxy, print each request as it completes, and put everything back on exit.\n" +
			"A simulator has no network settings of its own, so its capture also points this Mac's web proxy at sims.\n" +
			"Traffic from other apps on the Mac is relayed untouched unless --all is given.",
		Args: cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			return c.runProxy(ctx, d, proxyRunOptions{
				port: port, harPath: harPath, quiet: quiet, duration: duration, maxBody: maxBody, all: all, ssid: ssid, setup: setup,
			})
		}),
	}
	cmd.Flags().StringVar(&ssid, "ssid", "", "the wifi network an iPhone is on, when this Mac is not on it too")
	cmd.Flags().BoolVar(&setup, "setup", false, "send an iPhone its profile again, for a phone that lost it")
	cmd.Flags().IntVar(&port, "port", 0, "listen on this port instead of a free one")
	cmd.Flags().StringVar(&harPath, "har", "", "write the captured flows to this HAR file on exit")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "do not print each request; useful with --har")
	cmd.Flags().DurationVar(&duration, "for", 0, "stop after this long instead of waiting for an interrupt")
	cmd.Flags().IntVar(&maxBody, "max-body", proxy.DefaultMaxBody, "how much of each body to keep, in bytes")
	cmd.Flags().BoolVar(&all, "all", false, "also open this machine's own traffic, not just the device's")
	return cmd
}

type proxyRunOptions struct {
	port     int
	harPath  string
	quiet    bool
	duration time.Duration
	maxBody  int
	all      bool
	ssid     string
	setup    bool
}

func (c *cli) runProxy(ctx context.Context, d device.Device, o proxyRunOptions) error {
	scope := capture.ScopeDevice
	if o.all {
		scope = capture.ScopeAll
	}
	if o.ssid == "" {
		o.ssid = currentSSID(ctx)
	}
	session, err := c.Manager.StartCapture(ctx, d, capture.Options{
		Port:    o.port,
		MaxBody: o.maxBody,
		SSID:    o.ssid,
		Setup:   o.setup,
		Scope:   scope,
	})
	if err != nil {
		return err
	}
	// the capture owns the device's settings and this machine's; it comes down even on a signal
	stop := func() {
		if err := c.Manager.StopCapture(d); err != nil {
			fmt.Fprintln(c.Err, "sims:", err)
		}
	}
	defer stop()

	fmt.Fprintf(c.Err, "capturing %s on port %d\n", d.Name, session.Port)
	printSteps(c.Err, session.Steps)
	fmt.Fprintln(c.Err, "   press ctrl+c to stop and put everything back")
	if session.Fetched != nil {
		go c.settingsAfterFetch(ctx, d, session.Fetched)
	}

	// the store calls this from one goroutine per proxied connection, so the writer is shared:
	// without a lock the lines interleave and whole records are lost
	w := &flowWriter{out: c.Out, json: c.json}
	if !o.quiet {
		session.Store.OnDone(w.write)
	}

	// SIGTERM too: a capture killed by a script or a shell going away must still put the device and
	// this machine back, or they are left pointing at a proxy that no longer listens.
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if o.duration > 0 {
		timer := time.NewTimer(o.duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	} else {
		<-ctx.Done()
	}
	fmt.Fprintln(c.Err)
	if rec, ok := c.Manager.PhoneRecord(d); ok {
		fmt.Fprintf(c.Err, "   %s keeps sending its traffic to port %d; sims proxy standby %s relays it untouched, and so does the TUI while open\n", d.Name, rec.Port, d.ID)
	}

	if o.harPath != "" {
		f, err := os.Create(o.harPath)
		if err != nil {
			return err
		}
		flows := session.Flows()
		if err := proxy.WriteHAR(f, c.Version, flows); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintf(c.Err, "wrote %s\n", o.harPath)
	}
	// a pipe that closed part way through, `| head` being the usual one, stops the output after the
	// first failure; exiting 0 there would say everything was written
	if err := w.Err(); err != nil {
		return fmt.Errorf("the flow output was cut short: %w", err)
	}
	return nil
}

// flowWriter serialises what the proxy reports. Flows complete on many goroutines at once, and a
// caller reading --json as one record per line gets a short, silently truncated set otherwise.
type flowWriter struct {
	mu   sync.Mutex
	out  io.Writer
	json bool
	enc  *json.Encoder
	// failed is the first write error, kept so a broken pipe is reported once rather than per flow.
	failed error
}

func (w *flowWriter) write(f proxy.Flow) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed != nil {
		return
	}
	if w.json {
		if w.enc == nil {
			w.enc = json.NewEncoder(w.out)
		}
		w.failed = w.enc.Encode(f)
		return
	}
	_, w.failed = io.WriteString(w.out, flowLine(f))
}

// Err is the first write failure, so the command can exit non-zero rather than look like it wrote
// everything.
func (w *flowWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failed
}

// flowLine is the one-line human form of an exchange.
func flowLine(f proxy.Flow) string {
	status := fmt.Sprint(f.Status)
	switch {
	case f.Kind == proxy.KindTunnel:
		status = "tunnel"
	case f.Status == 0:
		status = "failed"
	}
	origin := ""
	if f.Origin == proxy.OriginHost {
		origin = " [host: " + f.Process + "]"
	}
	note := ""
	if f.Error != "" {
		note = "  " + f.Error
	}
	return fmt.Sprintf("%-6s %-6s %-9s %s%s%s\n",
		f.Method, status, proxy.SizeString(f.RespSize), f.URL, origin, note)
}

// proxyCleanCmd is the way out when a capture was killed and nothing has started one since: the
// machine is pointing at a proxy that is gone and the user needs one command, not a settings pane.
func (c *cli) proxyCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Put back what a capture that did not stop cleanly left behind",
		Args:  cobra.NoArgs,
		RunE: c.run(func(ctx context.Context, _ []string) error {
			l := c.Manager.Leftover()
			if !l.Found() {
				return c.result(map[string]bool{"cleaned": false}, "nothing to clean up")
			}
			if err := c.Manager.CleanLeftover(ctx, l); err != nil {
				return err
			}
			return c.result(map[string]bool{"cleaned": true}, "put back "+l.String())
		}),
	}
}

func (c *cli) proxyCACmd() *cobra.Command {
	var install bool
	cmd := &cobra.Command{
		Use:   "ca [device]",
		Short: "Print the proxy's root certificate, or install it on a device",
		Long: "Print the root certificate sims signs with. With a device and --install it is trusted there,\n" +
			"which is the part of a capture that survives between runs.",
		Args: cobra.MaximumNArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			dir, err := capture.DefaultCertDir()
			if err != nil {
				return err
			}
			ca, err := proxy.LoadOrCreateCA(dir)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if c.json {
					return c.printJSON(map[string]string{
						"path":        ca.Path(),
						"fingerprint": ca.Fingerprint(),
						"notAfter":    ca.NotAfter().Format(time.RFC3339),
					})
				}
				_, err := c.Out.Write(ca.CertPEM())
				return err
			}
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if !install {
				return fmt.Errorf("pass --install to trust the certificate on %s", d.Name)
			}
			p, err := c.Manager.Provider(d.Platform)
			if err != nil {
				return err
			}
			// this machine's own trust store needs a GUI authorisation panel, which only works from
			// a terminal; that is why it is a command rather than part of starting a capture
			if truster, ok := p.(interface {
				TrustCert(context.Context, []byte) error
			}); ok && d.IsHost() {
				if err := truster.TrustCert(ctx, ca.CertPEM()); err != nil {
					return err
				}
				return c.result(map[string]string{"trusted": d.Name},
					"trusted "+ca.Fingerprint()[:16]+" on "+d.Name)
			}
			proxier, ok := p.(device.Proxier)
			if !ok {
				return fmt.Errorf("%s cannot install a certificate from here", d.Platform)
			}
			// port 0 means "the certificate only": a provider must not point the device at
			// anything, since a device left on a dead proxy between here and a clearing call has
			// no network at all
			t := device.ProxyTarget{
				CACert: ca.CertPEM(), CACertDER: ca.CertDER(), CertName: "sims proxy CA",
				SSID: currentSSID(ctx), Signer: ca,
			}
			var fetched <-chan struct{}
			if d.Platform == device.PlatformIOS && d.Kind == device.KindPhysical {
				srv := &proxy.Server{CA: ca, Store: proxy.NewStore(0)}
				port, err := srv.Listen("0.0.0.0:0")
				if err != nil {
					return err
				}
				go srv.Serve()
				defer srv.Close()
				ip, err := capture.LANAddress()
				if err != nil {
					return err
				}
				t.Publish = func(name, contentType string, body []byte) (string, error) {
					fetched = srv.Publish("/"+name, contentType, body)
					return fmt.Sprintf("http://%s:%d/%s", ip, port, name), nil
				}
			}
			steps, err := proxier.SetProxy(ctx, d, t)
			if err != nil {
				return err
			}
			if c.json {
				if err := c.printJSON(steps); err != nil {
					return err
				}
			} else {
				printSteps(c.Out, steps)
			}
			if fetched == nil {
				return nil
			}
			fmt.Fprintln(c.Err, "   serving the profile until the phone fetches it; ctrl+c gives up")
			ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer cancel()
			select {
			case <-fetched:
				c.settingsAfterFetch(ctx, d, fetched)
			case <-ctx.Done():
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&install, "install", false, "trust the certificate on the named device")
	return cmd
}

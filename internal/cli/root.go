// Package cli is the non-interactive front end over sims.Manager, kept free of the TUI so it can ship as its own binary.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/doctor"
	"github.com/siner308/sims/internal/sims"
)

type Options struct {
	Version string
	Manager *sims.Manager
	// RunTUI is nil in a CLI-only build, where a bare `sims` prints help instead.
	RunTUI func(ctx context.Context) error
	Update func(ctx context.Context, checkOnly bool, w io.Writer) error
	Out    io.Writer
	Err    io.Writer
	// Args replaces os.Args[1:]; tests set it, and `go test` leaves its own flags in os.Args.
	Args []string
}

type cli struct {
	Options
	platform string
	json     bool
	now      func() time.Time
}

func New(o Options) *cobra.Command {
	c := &cli{Options: o, now: time.Now}
	if c.Out == nil {
		c.Out = os.Stdout
	}
	if c.Err == nil {
		c.Err = os.Stderr
	}
	root := &cobra.Command{
		Use:           "sims",
		Short:         "Android emulators, iOS simulators and real phones from one command",
		Long:          "sims drives adb, emulator, avdmanager, sdkmanager, xcrun simctl and xcrun devicectl behind one set of commands.\nWith no arguments it opens the terminal UI.",
		Version:       o.Version,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.RunTUI == nil {
				return cmd.Help()
			}
			return c.RunTUI(cmd.Context())
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			switch device.Platform(c.platform) {
			case "", device.PlatformAndroid, device.PlatformIOS:
				return nil
			}
			return fmt.Errorf("--platform must be android or ios, not %q", c.platform)
		},
	}
	root.SetVersionTemplate("sims {{.Version}}\n")
	root.SetOut(c.Out)
	root.SetErr(c.Err)
	root.PersistentFlags().StringVarP(&c.platform, "platform", "p", "", "only look at one platform: android or ios")
	root.PersistentFlags().BoolVar(&c.json, "json", false, "print the result as JSON")
	root.AddCommand(c.deviceCmd(), c.appCmd(), c.imageCmd(), c.deviceTypeCmd(), c.proxyCmd(), c.doctorCmd(), c.updateCmd(), c.skillCmd())
	return root
}

// group is a command that only holds subcommands. cobra treats a Run-less command as "print help" before
// it validates arguments, so `sims device reboot` would exit 0 with help; the RunE makes it an error.
func group(use, short string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Aliases: aliases,
		Short:   short,
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
}

// run turns usage off once arguments have parsed: a device that is not found is not a usage error.
func (c *cli) run(fn func(ctx context.Context, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return fn(cmd.Context(), args)
	}
}

func (c *cli) Platform() device.Platform { return device.Platform(c.platform) }

func (c *cli) resolve(ctx context.Context, ref string) (device.Device, error) {
	return c.Manager.Resolve(ctx, ref, c.Platform())
}

func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(c.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (c *cli) result(v any, human string) error {
	if c.json {
		return c.printJSON(v)
	}
	_, err := fmt.Fprintln(c.Out, human)
	return err
}

type table struct {
	w *tabwriter.Writer
}

func (c *cli) table(header ...string) *table {
	t := &table{w: tabwriter.NewWriter(c.Out, 0, 4, 2, ' ', 0)}
	fmt.Fprintln(t.w, strings.Join(header, "\t"))
	return t
}

func (t *table) row(cells ...string) {
	for i, cell := range cells {
		if cell == "" {
			cells[i] = "-"
		}
	}
	fmt.Fprintln(t.w, strings.Join(cells, "\t"))
}

func (t *table) flush() error { return t.w.Flush() }

func ago(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func (c *cli) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check adb, emulator, avdmanager, sdkmanager, aapt2, xcrun simctl/devicectl and idevicesyslog",
		Args:  cobra.NoArgs,
		RunE: c.run(func(ctx context.Context, _ []string) error {
			fmt.Fprintln(c.Out, "sims", c.Version)
			if !doctor.Run(ctx, c.Out, c.Manager.Providers()...) {
				return errExit
			}
			return nil
		}),
	}
}

func (c *cli) updateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replace this binary with the latest release",
		Args:  cobra.NoArgs,
		RunE: c.run(func(ctx context.Context, _ []string) error {
			if c.Update == nil {
				return fmt.Errorf("this build cannot update itself")
			}
			return c.Update(ctx, check, c.Out)
		}),
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	return cmd
}

// Execute runs the command line and returns the process exit status. Usage is printed only for
// errors raised before a command body ran (bad flags, wrong argument count); a device that is not
// found gets the message alone.
func Execute(ctx context.Context, o Options) int {
	root := New(o)
	root.SilenceUsage = true
	if o.Args != nil {
		root.SetArgs(o.Args)
	}
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	if err == errExit {
		return 1
	}
	errw := o.Err
	if errw == nil {
		errw = os.Stderr
	}
	fmt.Fprintln(errw, "sims:", err)
	if cmd != nil && !cmd.SilenceUsage {
		fmt.Fprintf(errw, "see '%s --help'\n", cmd.CommandPath())
	}
	return 1
}

// errExit is a failure that already explained itself on the output, so Execute exits 1 without a second message.
var errExit = errors.New("exit 1")

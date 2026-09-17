package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/siner308/sims/internal/device"
)

func (c *cli) appCmd() *cobra.Command {
	cmd := group("app", "List, install, launch and follow apps on a device", "apps")
	cmd.AddCommand(c.appListCmd(), c.appInstallCmd(), c.appUninstallCmd(), c.appLaunchCmd(), c.appLogsCmd())
	return cmd
}

func (c *cli) appListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list <device>",
		Aliases: []string{"ls"},
		Short:   "List the apps installed on a device",
		Args:    cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			apps, err := c.Manager.Apps(ctx, d)
			if err != nil {
				return err
			}
			if !all {
				apps = slices.DeleteFunc(apps, func(a device.App) bool { return a.System })
			}
			slices.SortStableFunc(apps, func(x, y device.App) int {
				if x.Running != y.Running {
					if x.Running {
						return -1
					}
					return 1
				}
				return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
			})
			// devicectl answers "success" with an empty list, so without this note an empty table
			// reads as sims having swallowed an error. It comes back empty on a phone that is
			// unlocked and plainly running the apps, so the note points elsewhere rather than
			// naming a cause: everything else about the device keeps working.
			if len(apps) == 0 && d.Platform == device.PlatformIOS && d.Kind == device.KindPhysical {
				fmt.Fprintf(c.Err, "devicectl listed no apps on %s. It reports only developer apps and returns nothing on some phones even when apps are installed; a USB connection is worth a try. Logs and the other device commands are unaffected.\n", d.Name)
			}
			if c.json {
				return c.printJSON(apps)
			}
			t := c.table("NAME", "BUNDLE ID", "VERSION", "STATE", "SOURCE")
			for _, a := range apps {
				state := ""
				if a.Running {
					state = "running"
				}
				t.row(a.Name, a.BundleID, a.Version, state, a.Source)
			}
			return t.flush()
		}),
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include preinstalled apps")
	return cmd
}

func (c *cli) appInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <device> <path>",
		Short: "Install an .apk or .app on a device",
		Args:  cobra.ExactArgs(2),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.InstallApp(ctx, d, args[1]); err != nil {
				return err
			}
			return c.result(d, "installed "+args[1]+" on "+d.Name)
		}),
	}
}

func (c *cli) appUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <device> <bundle-id>",
		Short: "Uninstall an app",
		Args:  cobra.ExactArgs(2),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.UninstallApp(ctx, d, args[1]); err != nil {
				return err
			}
			return c.result(d, "uninstalled "+args[1]+" from "+d.Name)
		}),
	}
}

func (c *cli) appLaunchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "launch <device> <bundle-id>",
		Short: "Launch an app",
		Args:  cobra.ExactArgs(2),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.LaunchApp(ctx, d, args[1]); err != nil {
				return err
			}
			return c.result(d, "launched "+args[1]+" on "+d.Name)
		}),
	}
}

func (c *cli) appLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <device> <bundle-id>",
		Short: "Stream one app's log lines to stdout",
		Long:  "Stream one app's log lines to stdout. On Android the app must be running (logcat filters by pid).",
		Args:  cobra.ExactArgs(2),
		RunE: c.run(func(ctx context.Context, args []string) error {
			return c.streamLogs(ctx, args[0], args[1])
		}),
	}
}

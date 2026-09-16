package cli

import (
	"context"
	"slices"

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
			if c.json {
				return c.printJSON(apps)
			}
			t := c.table("NAME", "BUNDLE ID", "VERSION", "SOURCE")
			for _, a := range apps {
				t.row(a.Name, a.BundleID, a.Version, a.Source)
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

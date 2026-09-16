package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

func (c *cli) deviceCmd() *cobra.Command {
	cmd := group("device", "List, boot, create and reach devices", "devices", "dev")
	cmd.AddCommand(
		c.deviceListCmd(), c.deviceGetCmd(), c.deviceBootCmd(), c.deviceWaitCmd(),
		c.deviceActionCmd("shutdown", "Shut a running device down", c.Manager.Shutdown),
		c.deviceActionCmd("erase", "Wipe a device's data; the device itself stays", c.Manager.Erase),
		c.deviceActionCmd("delete", "Delete a virtual device, or forget a paired phone", c.Manager.Delete),
		c.deviceCreateCmd(), c.deviceHardwareCmd(), c.deviceKeyCmd(),
		c.deviceScreenshotCmd(), c.deviceRebootCmd(),
		c.deviceConnectCmd(), c.deviceDisconnectCmd(), c.devicePairCmd(), c.deviceLogsCmd(),
	)
	return cmd
}

func (c *cli) deviceListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List devices on every platform",
		Args:    cobra.NoArgs,
		RunE: c.run(func(ctx context.Context, _ []string) error {
			devices, err := c.Manager.Devices(ctx)
			if platform := c.Platform(); platform != "" {
				devices = slices.DeleteFunc(devices, func(d device.Device) bool { return d.Platform != platform })
			}
			hidden := 0
			if !all {
				before := len(devices)
				devices = slices.DeleteFunc(devices, device.Device.NeverBooted)
				hidden = before - len(devices)
			}
			slices.SortStableFunc(devices, sims.DefaultOrder)
			if err != nil {
				fmt.Fprintln(c.Err, "sims:", err)
			}
			if c.json {
				return c.printJSON(devices)
			}
			c.printDevices(devices)
			if hidden > 0 {
				fmt.Fprintf(c.Err, "%d never-booted simulators hidden (--all shows them)\n", hidden)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include simulators that have never been booted")
	return cmd
}

func (c *cli) printDevices(devices []device.Device) {
	t := c.table("PLATFORM", "VIA", "NAME", "MODEL", "RUNTIME", "STATE", "LAST", "ID")
	for _, d := range devices {
		t.row(string(d.Platform), string(d.Transport), d.Name, d.Model, d.Runtime, string(d.State), ago(d.LastActiveAt, c.now()), d.ID)
	}
	t.flush()
}

func (c *cli) deviceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <device>",
		Short: "Show one device by id, adb serial or name",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if c.json {
				return c.printJSON(d)
			}
			c.printDevices([]device.Device{d})
			return nil
		}),
	}
}

func (c *cli) deviceBootCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "boot <device>",
		Short: "Boot a virtual device",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.Boot(ctx, d); err != nil {
				return err
			}
			if !wait {
				return c.result(d, "booting "+d.Name)
			}
			booted, err := c.Manager.WaitBooted(ctx, d, timeout)
			if err != nil {
				return err
			}
			return c.result(booted, "booted "+booted.Name)
		}),
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "return once the device reports booted")
	cmd.Flags().DurationVar(&timeout, "timeout", sims.BootTimeout, "how long --wait waits")
	return cmd
}

func (c *cli) deviceWaitCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait <device>",
		Short: "Wait until a device reports booted or connected",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			ready, err := c.Manager.WaitBooted(ctx, d, timeout)
			if err != nil {
				return err
			}
			return c.result(ready, ready.Name+" is "+strings.ToLower(string(ready.State)))
		}),
	}
	cmd.Flags().DurationVar(&timeout, "timeout", sims.BootTimeout, "how long to wait")
	return cmd
}

func (c *cli) deviceActionCmd(verb, short string, act func(context.Context, device.Device) error) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <device>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := act(ctx, d); err != nil {
				return err
			}
			return c.result(d, verb+" "+d.Name+": ok")
		}),
	}
}

func (c *cli) deviceCreateCmd() *cobra.Command {
	var imageRef, typeRef string
	var hw device.Hardware
	var boot bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a virtual device from an installed image",
		Long:  "Create a virtual device from an installed image. --image takes an id or name from `sims image list`; --type an id or name from `sims device-type list`, defaulting to a current phone.",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			name := args[0]
			img, err := c.Manager.ResolveImage(ctx, imageRef, c.Platform())
			if err != nil {
				return err
			}
			if !img.Installed {
				return fmt.Errorf("%s is not installed; run `sims image install %q` first", img.ID, img.ID)
			}
			types, err := c.Manager.DeviceTypes(ctx, img.Platform)
			if err != nil {
				return err
			}
			types = slices.DeleteFunc(types, func(t device.DeviceType) bool { return !t.Supports(img.Image) })
			var typ device.DeviceType
			if typeRef != "" {
				typ, err = sims.ResolveDeviceType(types, typeRef)
				if err != nil {
					return fmt.Errorf("%w among the types that run %s", err, img.Name)
				}
			} else if typ, _ = sims.DefaultDeviceType(types); typ.ID == "" {
				return fmt.Errorf("no device type runs %s; pass --type", img.Name)
			}
			var hwp *device.Hardware
			if hw != (device.Hardware{}) {
				if !c.Manager.CanEditHardware(img.Platform) {
					return fmt.Errorf("%s devices take no --ram, --cores or --disk", img.Platform)
				}
				hwp = &hw
			}
			created, err := c.Manager.Create(ctx, img, name, typ.ID, hwp)
			if err != nil {
				return err
			}
			if !boot {
				return c.result(created, fmt.Sprintf("created %s (%s) from %s", created.Name, created.ID, img.Name))
			}
			if err := c.Manager.Boot(ctx, created); err != nil {
				return err
			}
			booted, err := c.Manager.WaitBooted(ctx, created, timeout)
			if err != nil {
				return err
			}
			return c.result(booted, fmt.Sprintf("created and booted %s (%s)", booted.Name, booted.ID))
		}),
	}
	cmd.Flags().StringVarP(&imageRef, "image", "i", "", "image (system image or runtime) id or name")
	cmd.Flags().StringVarP(&typeRef, "type", "t", "", "device type id or name")
	cmd.Flags().IntVar(&hw.RAMMB, "ram", 0, "RAM in MB (android)")
	cmd.Flags().IntVar(&hw.Cores, "cores", 0, "CPU cores (android)")
	cmd.Flags().IntVar(&hw.DiskGB, "disk", 0, "data partition in GB (android)")
	cmd.Flags().BoolVar(&boot, "boot", false, "boot the device and wait for it")
	cmd.Flags().DurationVar(&timeout, "timeout", sims.BootTimeout, "how long --boot waits")
	cmd.MarkFlagRequired("image")
	return cmd
}

func (c *cli) deviceHardwareCmd() *cobra.Command {
	var hw device.Hardware
	cmd := &cobra.Command{
		Use:   "hardware <device>",
		Short: "Show or change RAM, cores and disk of an AVD",
		Long:  "Show RAM, cores and disk of an AVD; with --ram, --cores or --disk, change them. A running emulator picks the change up at its next boot.",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if hw != (device.Hardware{}) {
				if err := c.Manager.SetHardware(ctx, d, hw); err != nil {
					return err
				}
			}
			current, err := c.Manager.Hardware(ctx, d)
			if err != nil {
				return err
			}
			if c.json {
				return c.printJSON(current)
			}
			t := c.table("RAM_MB", "CORES", "DISK_GB")
			t.row(fmt.Sprint(current.RAMMB), fmt.Sprint(current.Cores), fmt.Sprint(current.DiskGB))
			return t.flush()
		}),
	}
	cmd.Flags().IntVar(&hw.RAMMB, "ram", 0, "RAM in MB")
	cmd.Flags().IntVar(&hw.Cores, "cores", 0, "CPU cores")
	cmd.Flags().IntVar(&hw.DiskGB, "disk", 0, "data partition in GB")
	return cmd
}

func (c *cli) deviceKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "key <device> <home|back|overview>",
		Short:     "Send a navigation key to a running device",
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{string(device.KeyHome), string(device.KeyBack), string(device.KeyOverview)},
		RunE: c.run(func(ctx context.Context, args []string) error {
			key := device.Key(args[1])
			switch key {
			case device.KeyHome, device.KeyBack, device.KeyOverview:
			default:
				return fmt.Errorf("key must be home, back or overview, not %q", args[1])
			}
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.SendKey(ctx, d, key); err != nil {
				return err
			}
			return c.result(d, string(key)+" sent to "+d.Name)
		}),
	}
}

func (c *cli) deviceScreenshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "screenshot <device> [path]",
		Short: "Save the screen of a running device as a PNG",
		Long:  "Save the screen of a running device as a PNG. Without a path the file is named after the device and the time; a path of - writes the image to stdout.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			png, err := c.Manager.Screenshot(ctx, d)
			if err != nil {
				return err
			}
			path := ""
			if len(args) == 2 {
				path = args[1]
			}
			if path == "-" {
				_, err := c.Out.Write(png)
				return err
			}
			if path == "" {
				path = screenshotName(d.Name, c.now())
			}
			if err := os.WriteFile(path, png, 0o644); err != nil {
				return err
			}
			return c.result(map[string]any{"path": path, "bytes": len(png)}, fmt.Sprintf("wrote %s (%d bytes)", path, len(png)))
		}),
	}
}

// screenshotName keeps the device name usable as a filename: a simulator name carries spaces and
// an adb serial carries a colon, which Windows refuses outright.
func screenshotName(name string, now time.Time) string {
	safe := strings.Map(func(r rune) rune {
		if strings.ContainsRune(` /\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("%s-%s.png", safe, now.Format("20060102-150405"))
}

func (c *cli) deviceRebootCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "reboot <device>",
		Short: "Restart a running device",
		Long:  "Restart a running device. A simulator shuts down and boots again, since simctl has no reboot of its own; an Android device and an iPhone restart in place.",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.Reboot(ctx, d); err != nil {
				return err
			}
			if !wait {
				return c.result(d, "rebooting "+d.Name)
			}
			booted, err := c.Manager.WaitBooted(ctx, d, timeout)
			if err != nil {
				return err
			}
			return c.result(booted, "rebooted "+booted.Name)
		}),
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "return once the device is back up")
	cmd.Flags().DurationVar(&timeout, "timeout", sims.BootTimeout, "how long --wait waits")
	return cmd
}

func (c *cli) deviceConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <device | host:port>",
		Short: "Reach a phone over wifi",
		Long: "Reach a phone over wifi. A device that is listed gets its wifi path opened: an iPhone's CoreDevice tunnel, or a USB Android phone switched to adb over tcp.\n" +
			"A host:port is handed to adb connect (Android 11+ wireless debugging, after `sims device pair host:port code`).",
		Args: cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if isHostPort(args[0]) {
				if err := c.Manager.ConnectAddress(ctx, c.Platform(), args[0]); err != nil {
					return err
				}
				return c.result(map[string]string{"address": args[0]}, "connected "+args[0])
			}
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			note, err := c.Manager.Connect(ctx, d)
			if err != nil {
				return err
			}
			return c.result(d, d.Name+": "+note)
		}),
	}
}

func (c *cli) deviceDisconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect <device>",
		Short: "Drop a wifi adb connection",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if err := c.Manager.Disconnect(ctx, d); err != nil {
				return err
			}
			return c.result(d, "disconnected "+d.Name)
		}),
	}
}

func (c *cli) devicePairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pair <device> | <host:port> <code>",
		Short: "Pair a phone with this machine",
		Long: "Pair a phone with this machine. An unpaired iPhone in the list pairs over USB the first time (accept the prompt on the phone).\n" +
			"An Android phone with wireless debugging on pairs by host:port and the code it shows.",
		Args: cobra.RangeArgs(1, 2),
		RunE: c.run(func(ctx context.Context, args []string) error {
			if len(args) == 2 {
				ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				if err := c.Manager.PairAddress(ctx, c.Platform(), args[0], args[1]); err != nil {
					return err
				}
				return c.result(map[string]string{"address": args[0]}, "paired "+args[0])
			}
			d, err := c.resolve(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(c.Err, "accept the pairing prompt on the phone (up to 2 minutes)...")
			if err := c.Manager.Pair(ctx, d); err != nil {
				return err
			}
			return c.result(d, "paired "+d.Name)
		}),
	}
}

func (c *cli) deviceLogsCmd() *cobra.Command {
	var appRef string
	cmd := &cobra.Command{
		Use:   "logs <device>",
		Short: "Stream the device log to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			return c.streamLogs(ctx, args[0], appRef)
		}),
	}
	cmd.Flags().StringVar(&appRef, "app", "", "only this app's lines (bundle id or name)")
	return cmd
}

func (c *cli) streamLogs(ctx context.Context, deviceRef, appRef string) error {
	d, err := c.resolve(ctx, deviceRef)
	if err != nil {
		return err
	}
	var app *device.App
	if appRef != "" {
		a, err := c.Manager.FindApp(ctx, d, appRef)
		if err != nil {
			return err
		}
		app = &a
	}
	cmd, err := c.Manager.LogCmd(ctx, d, app)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = c.Out, c.Err
	err = cmd.Run()
	// ctrl+c kills the child through the context; that is the normal way a log stream ends
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("log stream ended: %w", err)
	}
	return nil
}

func isHostPort(s string) bool {
	host, port, err := net.SplitHostPort(s)
	return err == nil && host != "" && port != ""
}

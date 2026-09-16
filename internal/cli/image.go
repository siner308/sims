package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

func (c *cli) imageCmd() *cobra.Command {
	cmd := group("image", "System images and simulator runtimes", "images", "img")
	cmd.AddCommand(c.imageListCmd(), c.imageInstallCmd())
	return cmd
}

func (c *cli) imageListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed images; --all adds what sdkmanager can still download",
		Args:    cobra.NoArgs,
		RunE: c.run(func(ctx context.Context, _ []string) error {
			images, err := c.Manager.Images(ctx)
			if platform := c.Platform(); platform != "" {
				images = slices.DeleteFunc(images, func(img sims.PlatformImage) bool { return img.Platform != platform })
			}
			if !all {
				images = slices.DeleteFunc(images, func(img sims.PlatformImage) bool { return !img.Installed })
			}
			if err != nil {
				fmt.Fprintln(c.Err, "sims:", err)
			}
			if c.json {
				return c.printJSON(images)
			}
			t := c.table("PLATFORM", "VERSION", "NAME", "INSTALLED", "ID")
			for _, img := range images {
				installed := "no"
				if img.Installed {
					installed = "yes"
				}
				t.row(string(img.Platform), img.Version, img.Name, installed, img.ID)
			}
			return t.flush()
		}),
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include images that are not installed")
	return cmd
}

func (c *cli) imageInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <image>",
		Short: "Install an Android system image (iOS runtimes come from xcodebuild -downloadPlatform)",
		Args:  cobra.ExactArgs(1),
		RunE: c.run(func(ctx context.Context, args []string) error {
			img, err := c.Manager.ResolveImage(ctx, args[0], c.Platform())
			if err != nil {
				return err
			}
			if img.Installed {
				return c.result(img, img.ID+" is already installed")
			}
			fmt.Fprintln(c.Err, "installing", img.ID, "(this can take minutes)...")
			if err := c.Manager.InstallImage(ctx, img); err != nil {
				return err
			}
			img.Installed = true
			return c.result(img, "installed "+img.ID)
		}),
	}
}

func (c *cli) deviceTypeCmd() *cobra.Command {
	var imageRef string
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the hardware profiles a virtual device can be created from",
		Args:    cobra.NoArgs,
		RunE: c.run(func(ctx context.Context, _ []string) error {
			type row struct {
				device.DeviceType
				Platform device.Platform `json:"platform"`
			}
			var rows []row
			platforms := c.Manager.Platforms()
			if platform := c.Platform(); platform != "" {
				platforms = []device.Platform{platform}
			}
			var img sims.PlatformImage
			if imageRef != "" {
				var err error
				if img, err = c.Manager.ResolveImage(ctx, imageRef, c.Platform()); err != nil {
					return err
				}
				platforms = []device.Platform{img.Platform}
			}
			for _, platform := range platforms {
				types, err := c.Manager.DeviceTypes(ctx, platform)
				if err != nil {
					return err
				}
				for _, t := range types {
					if imageRef != "" && !t.Supports(img.Image) {
						continue
					}
					rows = append(rows, row{DeviceType: t, Platform: platform})
				}
			}
			if c.json {
				return c.printJSON(rows)
			}
			t := c.table("PLATFORM", "NAME", "SCREEN", "RUNTIMES", "ID")
			for _, r := range rows {
				t.row(string(r.Platform), r.Name, r.Screen, runtimeRange(r.DeviceType), r.ID)
			}
			return t.flush()
		}),
	}
	list.Flags().StringVarP(&imageRef, "image", "i", "", "only types that can run this image")
	cmd := group("device-type", "Hardware profiles for new virtual devices", "device-types", "types")
	cmd.AddCommand(list)
	return cmd
}

func runtimeRange(t device.DeviceType) string {
	// simctl writes 65535.255.255 for "no upper bound"
	if strings.HasPrefix(t.MaxRuntime, "65535") {
		t.MaxRuntime = ""
	}
	switch {
	case t.MinRuntime == "" && t.MaxRuntime == "":
		return ""
	case t.MaxRuntime == "":
		return t.MinRuntime + "+"
	case t.MinRuntime == "":
		return "<=" + t.MaxRuntime
	}
	return t.MinRuntime + " to " + t.MaxRuntime
}

package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/siner308/sims/internal/device/android"
	"github.com/siner308/sims/internal/device/ios"
	"github.com/siner308/sims/internal/doctor"
	"github.com/siner308/sims/internal/ui"
	"github.com/siner308/sims/internal/update"
)

// version is set by GoReleaser via -ldflags; a `go install ...@vX.Y.Z` build carries it in the module info instead.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	v := resolveVersion()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version":
			fmt.Println("sims", v)
			return
		case "doctor":
			fmt.Println("sims", v)
			if !doctor.Run(context.Background(), os.Stdout, android.New(), ios.New()) {
				os.Exit(1)
			}
			return
		case "update":
			checkOnly := len(os.Args) > 2 && os.Args[2] == "--check"
			if err := update.Run(context.Background(), v, checkOnly, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "sims update:", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Println("usage: sims            start the TUI\n       sims doctor     check adb, emulator, avdmanager, sdkmanager, aapt2, xcrun simctl/devicectl, idevicesyslog\n       sims update     replace this binary with the latest release (--check only reports)\n       sims --version")
			return
		}
	}
	app := ui.New(v, android.New(), ios.New())
	if os.Getenv("SIMS_NO_UPDATE_CHECK") == "" {
		u := update.New()
		app.CheckUpdates(u.Latest, func(ctx context.Context, tag string) error {
			target, err := update.ExecutablePath()
			if err != nil {
				return err
			}
			return u.Apply(ctx, tag, target)
		})
	}
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

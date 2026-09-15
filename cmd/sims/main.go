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
		case "-h", "--help", "help":
			fmt.Println("usage: sims            start the TUI\n       sims doctor     check adb, emulator, avdmanager, sdkmanager, aapt2, xcrun simctl/devicectl, idevicesyslog\n       sims --version")
			return
		}
	}
	app := ui.New(v, android.New(), ios.New())
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

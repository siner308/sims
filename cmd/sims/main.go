package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/siner308/sims/internal/cli"
	"github.com/siner308/sims/internal/device/android"
	"github.com/siner308/sims/internal/device/ios"
	"github.com/siner308/sims/internal/sims"
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
	m := sims.New(android.New(), ios.New())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Execute(ctx, cli.Options{
		Version: v,
		Manager: m,
		RunTUI:  func(context.Context) error { return runTUI(v, m) },
		Update: func(ctx context.Context, checkOnly bool, w io.Writer) error {
			return update.Run(ctx, v, checkOnly, w)
		},
	})
	stop()
	os.Exit(code)
}

func runTUI(v string, m *sims.Manager) error {
	app := ui.New(v, m)
	if os.Getenv("SIMS_NO_UPDATE_CHECK") == "" {
		u := update.New()
		app.CheckUpdates(u.Latest, func(ctx context.Context, tag string) error {
			target, err := update.ExecutablePath()
			if err != nil {
				return err
			}
			if err := u.Apply(ctx, tag, target); err != nil {
				return err
			}
			update.RefreshSkill(ctx, target, io.Discard)
			return nil
		})
	}
	return app.Run()
}

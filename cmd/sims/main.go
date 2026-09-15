package main

import (
	"fmt"
	"os"

	"github.com/siner308/sims/internal/device/android"
	"github.com/siner308/sims/internal/device/ios"
	"github.com/siner308/sims/internal/ui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println("sims", version)
		return
	}
	app := ui.New(version, android.New(), ios.New())
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

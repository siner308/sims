//go:build !darwin && !windows

package desktop

import (
	"context"
	"errors"
	"os/exec"
)

// Linux is left out until its proxy side is written: listing the machine and then refusing to
// capture it would be worse than not listing it.
func supported() bool { return false }

func localName() string { return "" }

func osVersion(context.Context) string { return "" }

func hardwareModel(context.Context) string { return "" }

func hostLogCmd(context.Context) (*exec.Cmd, error) {
	return nil, errors.New("sims cannot follow this machine's log yet")
}

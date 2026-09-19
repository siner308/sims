//go:build !darwin && !windows

package desktop

import (
	"context"
	"errors"
)

func launchApp(context.Context, string) error {
	return errors.New("launching an app on this machine is not implemented here yet")
}

//go:build !darwin

package proxy

import (
	"context"
	"errors"
)

func LookupProcess(context.Context, string) (Process, error) {
	return Process{}, errors.New("process lookup is only implemented on macOS")
}

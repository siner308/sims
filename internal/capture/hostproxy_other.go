//go:build !darwin

package capture

import (
	"context"
	"errors"
)

type hostProxy struct{}

func (h *hostProxy) set(context.Context, string, int) error {
	return errors.New("pointing this machine at a proxy is only implemented on macOS")
}

func (h *hostProxy) restore() error { return nil }

func hostProxySupported() bool { return false }

func restoreRecordedProxy(journal) error { return nil }

func hostProxyStillSet(journal) bool { return false }

func (h *hostProxy) recordHostProxy() (string, []journalProxy) { return "", nil }

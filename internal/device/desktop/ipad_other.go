//go:build !darwin

package desktop

import "context"

func installApp(context.Context, string) error { return notSupported("install an app on") }

func uninstallApp(context.Context, string) error { return notSupported("uninstall an app from") }

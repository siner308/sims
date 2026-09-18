//go:build !darwin

package cli

import "context"

func currentSSID(context.Context) string { return "" }

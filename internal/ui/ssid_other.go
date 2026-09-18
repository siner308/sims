//go:build !darwin

package ui

import "context"

func currentSSID(context.Context) string { return "" }

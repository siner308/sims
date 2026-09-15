package device

import "context"

// Check is one toolchain requirement as seen from this machine.
type Check struct {
	Name     string
	Found    string // path or version when present
	OK       bool
	Optional bool   // missing only costs a feature, not the platform
	Hint     string // how to get it when missing
}

// Checker is implemented by providers that can explain what they need on the host.
type Checker interface {
	Checks(ctx context.Context) []Check
}

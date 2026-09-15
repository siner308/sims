package device

import "context"

type Check struct {
	Name     string
	Found    string // path or version when present
	OK       bool
	Optional bool   // missing only costs a feature, not the platform
	Hint     string // how to get it when missing
}

type Checker interface {
	Checks(ctx context.Context) []Check
}

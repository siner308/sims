package device

import "context"

type Check struct {
	Name     string
	Found    string // path or version when present
	OK       bool
	Optional bool   // missing only costs a feature, not the platform
	Hint     string // what is missing, in a sentence
	Fix      string // a command that installs it, when there is one
}

// Checks reports each requirement through emit as soon as it is known, so a slow probe
// (the first xcrun call after an Xcode update can take a while) still shows progress.
type Checker interface {
	Checks(ctx context.Context, emit func(Check))
}

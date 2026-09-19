//go:build !windows

package capture

import "syscall"

// sigZero asks the kernel whether a process exists without disturbing it.
var sigZero = syscall.Signal(0)

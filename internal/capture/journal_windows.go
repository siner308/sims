//go:build windows

package capture

import "os"

// Windows has no signal 0; FindProcess already fails for a pid that is gone.
var sigZero os.Signal = nil

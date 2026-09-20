//go:build windows

package ui

import "testing"

// A registry open command carries the program, then placeholders for the file. Only the program is
// wanted, and a path with spaces in it arrives quoted.
func TestExecutableIsTakenOutOfARegistryCommand(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{`"C:\Program Files\Editor\editor.exe" "%1"`, `C:\Program Files\Editor\editor.exe`},
		{`C:\Windows\notepad.exe %1`, `C:\Windows\notepad.exe`},
		{`notepad.exe`, `notepad.exe`},
		{`"C:\broken\unterminated.exe`, ``},
	} {
		if got := executableFromCommand(tc.command); got != tc.want {
			t.Errorf("executableFromCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

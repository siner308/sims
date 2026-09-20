//go:build !darwin && !windows

package ui

import (
	"strings"
	"testing"
)

// gio prints prose with the handler ids indented under it, so the ids have to be picked out rather
// than the output read as a list.
func TestDesktopIDsArePickedOutOfGioOutput(t *testing.T) {
	out := `Default application for "text/plain": org.gnome.TextEditor.desktop
Registered applications:
	org.gnome.TextEditor.desktop
	dev.zed.Zed.desktop
	code.desktop
Recommended applications:
	org.gnome.TextEditor.desktop
	code.desktop
`
	got := desktopIDs(out)
	want := []string{
		"org.gnome.TextEditor.desktop", "dev.zed.Zed.desktop", "code.desktop",
		"org.gnome.TextEditor.desktop", "code.desktop",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ids = %v, want %v", got, want)
	}

	// a machine with no handler prints prose and nothing else
	if ids := desktopIDs("No default applications for \"text/plain\"\n"); len(ids) != 0 {
		t.Errorf("prose was read as a handler: %v", ids)
	}
}

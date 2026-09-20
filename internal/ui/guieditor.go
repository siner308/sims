package ui

import "sort"

// guiEditor is a text editor that opens in a window of its own. A terminal editor cannot be used
// here: it would need the terminal sims is drawing on, and handing that over is what left the screen
// stranded when the editor exited.
type guiEditor struct {
	// Name is what the chooser shows.
	Name string
	// Open runs the editor on a file and returns once it has been launched, not when it is closed.
	Open func(path string) error
}

// sortEditors puts the list in a stable order so the chooser does not shuffle between openings.
func sortEditors(found []guiEditor) []guiEditor {
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}

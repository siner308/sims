package ui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// e offers the editors installed on the machine, and opening one is what enter does. A terminal
// editor cannot be used here, so the choice is always a windowed one.
func TestChoosingAnEditorOpensItAndIsRemembered(t *testing.T) {
	a, v, s, stop := streamFor(t, true)
	defer stop()

	send(s, "POST", "https://api.example.com/v1/login", 200, "", "")
	waitFor(t, a, 5*time.Second, func() bool {
		v.timeline.setFlows(s.Flows())
		return len(v.exchanges()) > 0
	})

	var mu sync.Mutex
	var opened []string
	fake := []guiEditor{
		{Name: "Alpha", Open: func(path string) error {
			mu.Lock()
			defer mu.Unlock()
			opened = append(opened, "Alpha:"+path)
			return nil
		}},
		{Name: "Beta", Open: func(string) error { return nil }},
	}

	// the chooser lists what it is given, which on a real machine is what the system reported
	var cv *editorChoiceView
	a.tv.QueueUpdate(func() {
		cv = newEditorChoiceView(a, fake, func(e guiEditor) { a.editor = &e; a.openIn(e, "/tmp/sample.txt") })
		a.push(cv)
	})
	waitFor(t, a, 5*time.Second, func() bool { return cv != nil })

	var rows []string
	onUIResult(a, func() bool {
		for r := 1; r < cv.table.GetRowCount(); r++ {
			rows = append(rows, cv.table.GetCell(r, 0).Text)
		}
		return true
	}, 5*time.Second)
	if strings.Join(rows, ",") != "Alpha,Beta" {
		t.Errorf("the chooser listed %v", rows)
	}

	a.tv.QueueUpdate(func() { cv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(opened) == 1
	})
	if !strings.HasPrefix(opened[0], "Alpha:") {
		t.Errorf("the wrong editor was opened: %q", opened[0])
	}

	// the choice is kept, so the next exchange opens without asking again
	var asked bool
	onUIResult(a, func() bool { asked = a.editor == nil; return true }, 5*time.Second)
	if asked {
		t.Error("the chosen editor was forgotten, so e would ask again every time")
	}
	// and the chooser closed itself, putting the reader back where they were
	onUIResult(a, func() bool { _, still := a.top().(*editorChoiceView); asked = still; return true }, 5*time.Second)
	if asked {
		t.Error("the chooser stayed open after picking")
	}
}

// Every editor offered has to open in a window of its own. One that takes over this terminal is the
// bug being fixed: quitting it leaves the reader on a screen sims no longer draws.
func TestOfferedEditorsAreWindowed(t *testing.T) {
	sample := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(sample, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, e := range guiEditors(sample) {
		for _, terminal := range []string{"nano", "vim", "vi", "emacs", "pico", "nvim"} {
			if strings.EqualFold(e.Name, terminal) {
				t.Errorf("%q is a terminal editor and would take the screen sims is drawing on", e.Name)
			}
		}
	}
}

// The remembered choice has to be changeable: a reader who picked the wrong editor, or who installs
// one afterwards, would otherwise be stuck with the first pick for the rest of the session.
func TestShiftEAsksAgain(t *testing.T) {
	a, _, _, stop := streamFor(t, true)
	defer stop()

	first := guiEditor{Name: "Alpha", Open: func(string) error { return nil }}
	onUIResult(a, func() bool { a.editor = &first; return true }, 5*time.Second)

	sample := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(sample, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// e with a choice already made opens it without asking
	a.tv.QueueUpdate(func() { a.chooseEditor(sample, false) })
	var asked bool
	onUIResult(a, func() bool { _, asked = a.top().(*editorChoiceView); return true }, 5*time.Second)
	if asked {
		t.Error("e asked again even though an editor was already chosen")
	}

	// shift+e opens the chooser so a different one can be picked
	a.tv.QueueUpdate(func() { a.chooseEditor(sample, true) })
	waitFor(t, a, 5*time.Second, func() bool {
		_, open := a.top().(*editorChoiceView)
		return open
	})
}

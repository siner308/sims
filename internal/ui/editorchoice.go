package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// editorChoiceView lists the editors this machine has and opens the file in the one picked. The
// list comes from the system, so it holds whatever is installed rather than a set of names sims
// carries around.
type editorChoiceView struct {
	app     *App
	table   *tview.Table
	editors []guiEditor
	onPick  func(guiEditor)
}

func newEditorChoiceView(a *App, editors []guiEditor, onPick func(guiEditor)) *editorChoiceView {
	v := &editorChoiceView{app: a, table: newTable(), editors: editors, onPick: onPick}
	v.table.SetTitle(" open in ")
	v.table.SetInputCapture(v.onKey)
	v.render()
	return v
}

func (v *editorChoiceView) Name() string               { return "editor" }
func (v *editorChoiceView) Primitive() tview.Primitive { return v.table }
func (v *editorChoiceView) Refresh()                   {}

func (v *editorChoiceView) Hints() []hint {
	return []hint{{"enter", "open in this editor"}, {"esc", "back"}}
}

func (v *editorChoiceView) render() {
	v.table.Clear()
	setHeader(v.table, "EDITOR")
	for i, e := range v.editors {
		v.table.SetCell(i+1, 0, tview.NewTableCell(e.Name).SetReference(e))
	}
	if len(v.editors) > 0 {
		v.table.Select(1, 0)
	}
}

func (v *editorChoiceView) selected() (guiEditor, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return guiEditor{}, false
	}
	e, ok := cell.GetReference().(guiEditor)
	return e, ok
}

func (v *editorChoiceView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() != tcell.KeyEnter {
		return ev
	}
	e, ok := v.selected()
	if !ok {
		return nil
	}
	v.app.pop()
	v.onPick(e)
	return nil
}

// chooseEditor opens the file in the editor already picked this session, and asks which one to use
// the first time. The choice is remembered so reading the next exchange takes one key; again is
// what a reader presses to pick a different one, including an editor installed since.
func (a *App) chooseEditor(path string, again bool) {
	if a.editor != nil && !again {
		a.openIn(*a.editor, path)
		return
	}
	found := guiEditors(path)
	if len(found) == 0 {
		a.flashErr(fmt.Errorf("no application on this machine is registered to open a text file"))
		return
	}
	a.push(newEditorChoiceView(a, found, func(e guiEditor) {
		a.editor = &e
		a.openIn(e, path)
	}))
}

// openIn launches the editor in its own window. It runs off the UI goroutine because starting an
// application can take a moment, and the stream behind it keeps drawing meanwhile.
func (a *App) openIn(e guiEditor, path string) {
	go func() {
		err := e.Open(path)
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.flashErr(fmt.Errorf("could not open %s: %w", e.Name, err))
				return
			}
			a.flash("opened in " + e.Name + "; the file is at " + path)
		})
	}()
}

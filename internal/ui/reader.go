package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// readerView shows one exchange as plain text on a page of its own, the way k9s shows a describe:
// esc goes back to where you were, and the text scrolls and searches without leaving sims. Handing
// the same text to $PAGER or $EDITOR is still there for folding and copying out, on enter and e.
type readerView struct {
	app   *App
	text  *tview.TextView
	title string
	body  string
	// found is the search term currently highlighted, kept so n can step through the same one.
	found string
	hits  []int
	hit   int
	wrap  bool
}

func newReaderView(a *App, title, body string) *readerView {
	v := &readerView{app: a, title: title, body: body}
	v.text = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(false)
	v.text.SetBorder(true).SetTitle(" " + title + " ").SetTitleColor(titleColor())
	v.text.SetInputCapture(v.onKey)
	v.render()
	return v
}

func (v *readerView) Name() string               { return "read" }
func (v *readerView) Primitive() tview.Primitive { return v.text }
func (v *readerView) Refresh()                   {}

func (v *readerView) Hints() []hint {
	return []hint{
		{"/", "find"}, {"n", "next match"}, {"shift+n", "previous"},
		{"w", "toggle wrap"}, {"g", "top"}, {"shift+g", "bottom"},
		groupBreak,
		{"enter", "read in $PAGER"}, {"e", "open in an editor"}, {"shift+e", "pick another editor"}, {"esc", "back"},
	}
}

func (v *readerView) render() {
	if v.found == "" {
		v.text.SetText(tview.Escape(v.body))
		return
	}
	// the match under the cursor is marked differently from the rest, so n visibly steps
	var b strings.Builder
	rest, low, term := v.body, strings.ToLower(v.body), strings.ToLower(v.found)
	at, n := 0, 0
	for {
		i := strings.Index(low[at:], term)
		if i < 0 {
			b.WriteString(tview.Escape(rest[at:]))
			break
		}
		i += at
		b.WriteString(tview.Escape(rest[at : i+0]))
		colour := "[black:yellow]"
		if n == v.hit {
			colour = "[black:aqua]"
		}
		b.WriteString(colour + tview.Escape(rest[i:i+len(term)]) + "[-:-]")
		at = i + len(term)
		n++
	}
	v.text.SetText(b.String())
}

// find records where every match is, so n and shift+n can walk them without searching again.
func (v *readerView) find(term string) {
	v.found, v.hits, v.hit = term, nil, 0
	if term == "" {
		v.render()
		return
	}
	low, t := strings.ToLower(v.body), strings.ToLower(term)
	for at := 0; ; {
		i := strings.Index(low[at:], t)
		if i < 0 {
			break
		}
		v.hits = append(v.hits, at+i)
		at += i + len(t)
	}
	if len(v.hits) == 0 {
		v.app.flashErr(fmt.Errorf("%q is not in this exchange", term))
		v.found = ""
		v.render()
		return
	}
	v.render()
	v.scrollToHit()
}

func (v *readerView) step(by int) {
	if len(v.hits) == 0 {
		return
	}
	v.hit = (v.hit + by + len(v.hits)) % len(v.hits)
	v.render()
	v.scrollToHit()
}

// scrollToHit puts the current match a few lines down from the top, so its context is visible.
func (v *readerView) scrollToHit() {
	line := strings.Count(v.body[:v.hits[v.hit]], "\n")
	if line > 3 {
		line -= 3
	} else {
		line = 0
	}
	v.text.ScrollTo(line, 0)
}

func (v *readerView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch {
	case ev.Rune() == '/':
		v.app.prompt("find:", v.found, v.find)
	case ev.Rune() == 'n':
		v.step(1)
	case ev.Rune() == 'N':
		v.step(-1)
	case ev.Rune() == 'w':
		v.wrap = !v.wrap
		v.text.SetWrap(v.wrap)
	case ev.Rune() == 'g':
		v.text.ScrollToBeginning()
	case ev.Rune() == 'G':
		v.text.ScrollToEnd()
	case ev.Key() == tcell.KeyEnter:
		v.app.openInPager(v.title, v.body)
	case ev.Rune() == 'e', ev.Rune() == 'E':
		v.app.openInEditor(v.title, v.body, ev.Rune() == 'E')
	default:
		return ev
	}
	return nil
}

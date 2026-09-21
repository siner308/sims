package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/proxy"
)

type typesView struct {
	app   *App
	table *tview.Table
	flows func() []proxy.Flow
}

func newTypesView(a *App, flows func() []proxy.Flow) *typesView {
	v := &typesView{app: a, table: newTable(), flows: flows}
	v.table.SetInputCapture(v.onKey)
	v.table.SetTitle(" types ")
	return v
}

func (v *typesView) Name() string               { return "types" }
func (v *typesView) Primitive() tview.Primitive { return v.table }
func (v *typesView) Hints() []hint {
	return []hint{
		{"space", "show or hide"}, {"o", "only this one"}, {"a", "show all"}, {"esc", "back"},
	}
}

func (v *typesView) Refresh() { v.render() }

func (v *typesView) render() {
	row, _ := v.table.GetSelection()
	counts := map[proxy.Resource]int{}
	for _, f := range v.flows() {
		counts[f.Resource()]++
	}
	v.table.Clear()
	setHeader(v.table, "", "TYPE", "COUNT", "WHAT")
	for i, r := range proxy.Resources {
		mark, color := "[x]", tcell.ColorDefault
		if v.app.hiddenTypes[r] {
			mark, color = "[ ]", tcell.ColorGray
		}
		// tview reads "[x]" as a colour tag and drops it; escaped, the brackets reach the screen
		v.table.SetCell(i+1, 0, tview.NewTableCell(tview.Escape(mark)).SetReference(r).SetTextColor(color))
		v.table.SetCell(i+1, 1, tview.NewTableCell(string(r)).SetTextColor(color))
		v.table.SetCell(i+1, 2, tview.NewTableCell(fmt.Sprint(counts[r])).SetTextColor(color).SetAlign(tview.AlignRight))
		v.table.SetCell(i+1, 3, tview.NewTableCell(describe(r)).SetTextColor(tcell.ColorGray).SetExpansion(1))
	}
	if row < 1 {
		row = 1
	}
	v.table.Select(row, 0)
}

func describe(r proxy.Resource) string {
	switch r {
	case proxy.ResourceXHR:
		return "API calls: json, xml, protobuf, forms, anything with no other type"
	case proxy.ResourceDoc:
		return "html pages"
	case proxy.ResourceJS:
		return "scripts"
	case proxy.ResourceCSS:
		return "stylesheets"
	case proxy.ResourceImg:
		return "images, including svg and icons"
	case proxy.ResourceFont:
		return "web fonts"
	case proxy.ResourceMedia:
		return "audio, video, HLS playlists and segments"
	case proxy.ResourceWS:
		return "websockets"
	}
	return "tunnels sims could not open, and binaries with no known type"
}

func (v *typesView) selected() (proxy.Resource, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return "", false
	}
	r, ok := cell.GetReference().(proxy.Resource)
	return r, ok
}

func (v *typesView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() == tcell.KeyEscape {
		v.app.pop()
		return nil
	}
	switch ev.Rune() {
	case ' ':
		if r, ok := v.selected(); ok {
			v.app.hiddenTypes[r] = !v.app.hiddenTypes[r]
		}
	case 'o':
		if r, ok := v.selected(); ok {
			for _, other := range proxy.Resources {
				v.app.hiddenTypes[other] = other != r
			}
		}
	case 'a':
		v.app.hiddenTypes = map[proxy.Resource]bool{}
	default:
		if ev.Key() == tcell.KeyEnter {
			if r, ok := v.selected(); ok {
				v.app.hiddenTypes[r] = !v.app.hiddenTypes[r]
			}
			break
		}
		return ev
	}
	v.render()
	return nil
}

// names the shorter of the kept and the hidden buckets, since a reader chose one side or the other
func (a *App) typesNote() string {
	if len(a.hiddenTypes) == 0 {
		return ""
	}
	var kept, hidden []string
	for _, r := range proxy.Resources {
		if a.hiddenTypes[r] {
			hidden = append(hidden, "-"+string(r))
		} else {
			kept = append(kept, string(r))
		}
	}
	if len(kept) == 0 {
		return "[gray]types: none (y)[-] "
	}
	if len(kept) <= len(hidden) {
		return "[gray]types: " + join(kept) + " (y)[-] "
	}
	return "[gray]types: " + join(hidden) + " (y)[-] "
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

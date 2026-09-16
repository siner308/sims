package ui

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/sims"
)

const minHeaderHeight = 6

type hint struct {
	key   string
	label string
}

// groupBreak starts a new column in the hotkey block, so related keys stay together instead of wrapping.
var groupBreak = hint{}

func (h hint) isBreak() bool { return h.key == "" && h.label == "" }

var globalHints = []hint{
	{":", "command"}, {"?", "help"}, {"esc", "back"}, {"r", "refresh"}, {"ctrl+c", "quit"},
}

var navigationHints = []hint{
	{"j", "down"}, {"k", "up"}, {"g", "top"}, {"shift+g", "bottom"}, {"ctrl+f", "page down"}, {"ctrl+b", "page up"},
}

const logoText = ` ___(_)_ __ ___  ___
/ __| | '_ ' _ \/ __|
\__ \ | | | | | \__ \
|___/_|_| |_| |_|___/`

type header struct {
	flex      *tview.Flex
	info      *tview.TextView
	keys      *tview.TextView
	logo      *tview.TextView
	version   string
	facts     [][2]string
	usage     usage
	height    int
	hints     []hint
	keysWidth int
}

func newHeader(version string) *header {
	h := &header{
		flex:    tview.NewFlex().SetDirection(tview.FlexColumn),
		info:    tview.NewTextView().SetDynamicColors(true).SetWrap(false),
		keys:    tview.NewTextView().SetDynamicColors(true).SetWrap(false),
		logo:    tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight),
		version: version,
	}
	h.setUpdate("")
	h.flex.AddItem(h.info, 60, 0, false).
		AddItem(h.keys, 0, 1, false).
		AddItem(h.logo, 24, 0, false)
	// the panel only learns its width while drawing, so the column fit is decided there
	h.keys.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		if width != h.keysWidth {
			h.keysWidth = width
			h.keys.SetText(renderHints(h.hints, width))
		}
		return x, y, width, height
	})
	return h
}

// setUpdate writes the version under the logo, with the newer release and how to get it when tag is set.
func (h *header) setUpdate(tag string) {
	text := "[yellow]" + tview.Escape(logoText) + "[-]\n[gray]" + tview.Escape(h.version) + "[-]"
	if tag != "" {
		text += "\n[yellow]" + tview.Escape(tag) + " available[-]\n[gray]:update[-]"
	}
	h.logo.SetText(text)
}

// loadFacts gathers tool facts off the UI goroutine and hands them to onDone; the caller stores them
// on the UI goroutine, because draw reads h.facts there on every usage tick.
func (h *header) loadFacts(ctx context.Context, m *sims.Manager, onDone func(facts [][2]string)) {
	platforms := m.Platforms()
	names := make([]string, len(platforms))
	for i, p := range platforms {
		names[i] = string(p)
	}
	base := [][2]string{{"Platforms", strings.Join(names, ", ")}}
	for _, p := range slices.Sorted(maps.Keys(m.Missing())) {
		base = append(base, [2]string{string(p), "[gray]" + tview.Escape(m.Missing()[p].Error()) + "[-]"})
	}
	go func() {
		facts := base
		var tools []string
		for _, p := range platforms {
			fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			for _, f := range m.Info(fctx, p) {
				if f[0] == "Android SDK" {
					facts = append(facts, [2]string{f[0], shortenHome(f[1])})
					continue
				}
				tools = append(tools, f[0]+" "+f[1])
			}
			cancel()
		}
		if len(tools) > 0 {
			facts = append(facts, [2]string{"Tools", strings.Join(tools, ", ")})
		}
		onDone(facts)
	}()
}

// draw returns the number of lines the header needs; the app resizes the header row to that.
func (h *header) draw(hints []hint) int {
	facts := append(append([][2]string(nil), h.facts...), h.usage.facts()...)
	info := renderFacts(facts)
	// global keys lead so that a narrow terminal drops the sort column, never quit or help
	all := append(append([]hint(nil), globalHints...), groupBreak)
	h.hints = append(all, hints...)
	keys := renderHints(h.hints, h.keysWidth)
	h.info.SetText(info)
	h.keys.SetText(keys)
	h.flex.ResizeItem(h.info, factsWidth(facts)+2, 0)
	h.height = max(minHeaderHeight, lineCount(info), lineCount(keys), lineCount(h.logo.GetText(true)))
	return h.height
}

func factsWidth(facts [][2]string) int {
	label, value := 0, 0
	for _, f := range facts {
		label = max(label, len(f[0])+1)
		value = max(value, len(tview.Escape(stripTags(f[1]))))
	}
	return label + 1 + value
}

func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func lineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func renderFacts(facts [][2]string) string {
	width := 0
	for _, f := range facts {
		width = max(width, len(f[0]))
	}
	var b strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&b, "[orange]%-*s[-] [white]%s[-]\n", width+1, f[0]+":", f[1])
	}
	return b.String()
}

// Each group is one column as tall as the group itself, so nothing wraps and nothing is split.
// Columns that would not fit in width are left out whole; width <= 0 means unknown and keeps them all.
func renderHints(hints []hint, width int) string {
	var columns [][]hint
	var cur []hint
	for _, hn := range hints {
		if hn.isBreak() {
			if len(cur) > 0 {
				columns = append(columns, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, hn)
	}
	if len(cur) > 0 {
		columns = append(columns, cur)
	}
	if len(columns) == 0 {
		return ""
	}
	keyWidth := make([]int, len(columns))
	labelWidth := make([]int, len(columns))
	for c, col := range columns {
		for _, hn := range col {
			keyWidth[c] = max(keyWidth[c], len(hn.key)+2)
			labelWidth[c] = max(labelWidth[c], len(hn.label))
		}
	}
	if width > 0 {
		used := 0
		for c := range columns {
			used += keyWidth[c] + 1 + labelWidth[c] + 2
			if used > width {
				columns = columns[:c]
				break
			}
		}
	}
	rows := 0
	for _, col := range columns {
		rows = max(rows, len(col))
	}
	var b strings.Builder
	for r := 0; r < rows; r++ {
		for c, col := range columns {
			if r >= len(col) {
				fmt.Fprintf(&b, "%-*s  ", keyWidth[c]+1+labelWidth[c], "")
				continue
			}
			hn := col[r]
			fmt.Fprintf(&b, "[aqua]%-*s[-] [gray]%-*s[-]  ", keyWidth[c], "<"+tview.Escape(hn.key)+">", labelWidth[c], hn.label)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func titleColor() tcell.Color { return tcell.ColorFuchsia }

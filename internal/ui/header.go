package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
)

const headerHeight = 6

type hint struct {
	key   string
	label string
}

var globalHints = []hint{
	{":", "command"}, {"?", "help"}, {"esc", "back"}, {"r", "refresh"}, {"ctrl+c", "quit"},
}

var navigationHints = []hint{
	{"j", "down"}, {"k", "up"}, {"g", "top"}, {"G", "bottom"}, {"ctrl+f", "page down"}, {"ctrl+b", "page up"},
}

const logoText = `     _
 ___(_)_ __ ___  ___
/ __| | '_ ' _ \/ __|
\__ \ | | | | | \__ \
|___/_|_| |_| |_|___/`

type header struct {
	flex  *tview.Flex
	info  *tview.TextView
	keys  *tview.TextView
	logo  *tview.TextView
	facts [][2]string
	usage usage
}

func newHeader(version string) *header {
	h := &header{
		flex: tview.NewFlex().SetDirection(tview.FlexColumn),
		info: tview.NewTextView().SetDynamicColors(true),
		keys: tview.NewTextView().SetDynamicColors(true),
		logo: tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight),
	}
	h.logo.SetText("[yellow]" + tview.Escape(logoText) + "[-]\n[gray]" + tview.Escape(version) + "[-]")
	h.flex.AddItem(h.info, 60, 0, false).
		AddItem(h.keys, 0, 1, false).
		AddItem(h.logo, 24, 0, false)
	return h
}

func (h *header) loadFacts(ctx context.Context, providers map[device.Platform]device.Provider, missing map[device.Platform]error, onDone func()) {
	platforms := make([]string, 0, len(providers))
	for p := range providers {
		platforms = append(platforms, string(p))
	}
	sort.Strings(platforms)
	base := [][2]string{{"Platforms", strings.Join(platforms, ", ")}}
	for p, err := range missing {
		base = append(base, [2]string{string(p), "[gray]" + tview.Escape(err.Error()) + "[-]"})
	}
	go func() {
		facts := base
		var tools []string
		for _, name := range platforms {
			if d, ok := providers[device.Platform(name)].(device.Describer); ok {
				fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				for _, f := range d.Info(fctx) {
					if f[0] == "Android SDK" {
						facts = append(facts, f)
						continue
					}
					tools = append(tools, f[0]+" "+f[1])
				}
				cancel()
			}
		}
		if len(tools) > 0 {
			facts = append(facts, [2]string{"Tools", strings.Join(tools, ", ")})
		}
		h.facts = facts
		onDone()
	}()
}

// The panel is headerHeight lines tall, so usage goes last and everything above it is kept to three lines.
func (h *header) draw(hints []hint) {
	h.info.SetText(renderFacts(append(append([][2]string(nil), h.facts...), h.usage.facts()...)))
	h.keys.SetText(renderHints(append(append([]hint(nil), hints...), globalHints...), headerHeight))
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

// Columns fill top to bottom, so index i sits at column i/rows, row i%rows.
func renderHints(hints []hint, rows int) string {
	if len(hints) == 0 {
		return ""
	}
	cols := (len(hints) + rows - 1) / rows
	keyWidth := make([]int, cols)
	labelWidth := make([]int, cols)
	for i, hn := range hints {
		c := i / rows
		keyWidth[c] = max(keyWidth[c], len(hn.key)+2)
		labelWidth[c] = max(labelWidth[c], len(hn.label))
	}
	var b strings.Builder
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			i := c*rows + r
			if i >= len(hints) {
				break
			}
			hn := hints[i]
			fmt.Fprintf(&b, "[aqua]%-*s[-] [gray]%-*s[-]  ", keyWidth[c], "<"+tview.Escape(hn.key)+">", labelWidth[c], hn.label)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func titleColor() tcell.Color { return tcell.ColorFuchsia }

package ui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

type helpView struct {
	text *tview.TextView
}

type helpSection struct {
	title string
	hints []hint
}

func newHelpView(a *App) *helpView {
	t := tview.NewTextView().SetDynamicColors(true)
	t.SetBorder(true).SetTitle(" help ").SetTitleColor(titleColor())

	var resource []hint
	var resourceName string
	if len(a.stack) > 0 {
		resourceName = a.top().Name()
		resource = a.top().Hints()
	}
	sections := []helpSection{
		{strings.ToUpper(resourceName), resource},
		{"GENERAL", append(append([]hint(nil), globalHints...), []hint{
			{":dev", "devices (root)"}, {":apps", "apps of selected device"}, {":logs", "logs of selected device"},
			{":img", "images / new device"}, {":connect HOST:PORT", "adb connect"}, {":pair HOST:PORT CODE", "adb pair"},
		}...)},
		{"NAVIGATION", navigationHints},
	}
	t.SetText(renderHelp(sections))
	return &helpView{text: t}
}

func renderHelp(sections []helpSection) string {
	rows := 0
	keyW := make([]int, len(sections))
	labelW := make([]int, len(sections))
	for i, sec := range sections {
		rows = max(rows, len(sec.hints))
		keyW[i] = len(sec.title)
		for _, h := range sec.hints {
			keyW[i] = max(keyW[i], len(h.key)+2)
			labelW[i] = max(labelW[i], len(h.label))
		}
	}
	var b strings.Builder
	b.WriteString("\n")
	for i, sec := range sections {
		fmt.Fprintf(&b, " [green::b]%-*s[-::-]  ", keyW[i]+labelW[i]+1, sec.title)
	}
	b.WriteString("\n")
	for r := 0; r < rows; r++ {
		for i, sec := range sections {
			if r < len(sec.hints) {
				h := sec.hints[r]
				fmt.Fprintf(&b, " [aqua]%-*s[-] [gray]%-*s[-]  ", keyW[i], "<"+tview.Escape(h.key)+">", labelW[i], h.label)
			} else {
				fmt.Fprintf(&b, " %-*s  ", keyW[i]+labelW[i]+1, "")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n [gray]android: adb, emulator, avdmanager, sdkmanager via ANDROID_HOME\n")
	b.WriteString(" ios:     xcrun simctl (simulators), xcrun devicectl (devices), idevicesyslog for device logs[-]\n")
	return b.String()
}

func (v *helpView) Name() string               { return "help" }
func (v *helpView) Primitive() tview.Primitive { return v.text }
func (v *helpView) Hints() []hint              { return nil }
func (v *helpView) Refresh()                   {}

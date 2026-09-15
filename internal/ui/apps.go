package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
)

type appsView struct {
	app        *App
	dev        device.Device
	table      *tview.Table
	apps       []device.App
	filter     string
	showSystem bool
}

func newAppsView(a *App, d device.Device) *appsView {
	v := &appsView{app: a, dev: d, table: newTable()}
	v.table.SetTitle(fmt.Sprintf(" apps @ %s ", d.Name))
	v.table.SetInputCapture(v.onKey)
	return v
}

func (v *appsView) Name() string               { return "apps" }
func (v *appsView) Primitive() tview.Primitive { return v.table }
func (v *appsView) Hints() []hint {
	return []hint{
		{"enter", "launch"}, {"i", "install (os dialog)"}, {"shift+i", "install (tui picker)"}, {"ctrl+u", "uninstall"},
		groupBreak,
		{"s", "toggle preinstalled"}, {"l", "logs of this app"}, {"/", "filter"},
		groupBreak,
		{"h", "home key"}, {"backspace", "back key"}, {"o", "overview key"},
	}
}

func (v *appsView) Refresh() {
	p, err := v.app.providerFor(v.dev)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	v.app.setStatus(" loading apps...")
	var apps []device.App
	v.app.async(func() error {
		var err error
		apps, err = p.Apps(v.app.ctx, v.dev)
		return err
	}, func() {
		v.apps = apps
		v.render()
		v.app.setStatus("")
	})
}

func (v *appsView) render() {
	v.table.Clear()
	setHeader(v.table, "NAME", "BUNDLE ID", "VERSION", "SOURCE")
	r, hidden := 1, 0
	for _, a := range v.apps {
		if a.System && !v.showSystem {
			hidden++
			continue
		}
		if v.filter != "" && !strings.Contains(strings.ToLower(a.Name+a.BundleID), strings.ToLower(v.filter)) {
			continue
		}
		source := a.Source
		if a.System {
			source = "[gray]" + source + "[-]"
		}
		v.table.SetCell(r, 0, tview.NewTableCell(highlight(a.Name, v.filter)).SetReference(a))
		v.table.SetCell(r, 1, tview.NewTableCell(highlight(a.BundleID, v.filter)))
		v.table.SetCell(r, 2, tview.NewTableCell(a.Version))
		v.table.SetCell(r, 3, tview.NewTableCell(source))
		r++
	}
	if r == 1 {
		msg := "[gray]no apps[-]"
		if hidden > 0 {
			msg = fmt.Sprintf("[gray]nothing installed by you (%d preinstalled apps hidden, press s)[-]", hidden)
		}
		v.table.SetCell(1, 0, tview.NewTableCell(msg).SetSelectable(false))
	}
	v.table.Select(1, 0)
	title := fmt.Sprintf(" apps @ %s [%d] ", v.dev.Name, r-1)
	if v.showSystem {
		title = fmt.Sprintf(" apps @ %s [%d, +preinstalled] ", v.dev.Name, r-1)
	}
	if v.filter != "" {
		title += fmt.Sprintf("/%s ", v.filter)
	}
	v.table.SetTitle(title)
}

func (v *appsView) selected() (device.App, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return device.App{}, false
	}
	a, ok := cell.GetReference().(device.App)
	return a, ok
}

func (v *appsView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	p, err := v.app.providerFor(v.dev)
	if err != nil {
		v.app.flashErr(err)
		return nil
	}
	switch {
	case ev.Key() == tcell.KeyEnter:
		if a, ok := v.selected(); ok {
			v.app.async(func() error { return p.LaunchApp(v.app.ctx, v.dev, a.BundleID) }, func() {
				v.app.flash("launched " + a.BundleID)
			})
		}
	case ev.Rune() == 'i':
		v.pickAndInstall(p, true)
	case ev.Rune() == 'I':
		v.pickAndInstall(p, false)
	case ev.Key() == tcell.KeyCtrlU:
		if a, ok := v.selected(); ok {
			v.app.confirm(fmt.Sprintf("uninstall %s?", a.BundleID), func() {
				v.app.async(func() error { return p.UninstallApp(v.app.ctx, v.dev, a.BundleID) }, func() {
					v.app.flash("uninstalled " + a.BundleID)
					v.Refresh()
				})
			})
		}
	case ev.Rune() == 's':
		v.showSystem = !v.showSystem
		v.render()
	case ev.Rune() == 'h':
		v.app.sendKey(v.dev, device.KeyHome)
	case ev.Rune() == 'o':
		v.app.sendKey(v.dev, device.KeyOverview)
	case ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		v.app.sendKey(v.dev, device.KeyBack)
	case ev.Rune() == '/':
		v.app.prompt("filter:", "", func(f string) { v.filter = f; v.render() })
	case ev.Rune() == 'l':
		if a, ok := v.selected(); ok {
			v.app.push(newLogsView(v.app, v.dev, &a))
		} else {
			v.app.replaceTop(newLogsView(v.app, v.dev, nil))
		}
	default:
		return ev
	}
	return nil
}

// The OS dialog runs off the UI goroutine; a platform without one falls back to the TUI picker.
func (v *appsView) pickAndInstall(p device.Provider, native bool) {
	install := func(path string) {
		v.app.setStatus(" installing " + path + "...")
		v.app.async(func() error { return p.InstallApp(v.app.ctx, v.dev, path) }, func() {
			v.app.flash("installed " + path)
			v.Refresh()
		})
	}
	exts := installableExts(v.dev)
	if !native {
		v.app.push(newPickerView(v.app, exts, install))
		return
	}
	v.app.setStatus(" waiting for the file dialog...")
	var path string
	go func() {
		var err error
		path, err = nativePick(v.app.ctx, exts)
		v.app.tv.QueueUpdateDraw(func() {
			switch {
			case errors.Is(err, errNativePickerUnsupported):
				v.app.push(newPickerView(v.app, exts, install))
			case errors.Is(err, errNativePickerCancelled):
				v.app.setStatus("")
			case err != nil:
				v.app.flashErr(err)
			default:
				install(path)
			}
		})
	}()
}

// devicectl's install help names only .app bundles, the same unit simctl installs, so both iOS kinds take .app.
func installableExts(d device.Device) []string {
	if d.Platform == device.PlatformAndroid {
		return []string{".apk"}
	}
	return []string{".app"}
}

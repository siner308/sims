package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/capture"
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
	// sims does not install onto the machine it runs on, so this list is what is here already
	if v.dev.IsHost() {
		return []hint{
			{"enter", "launch"}, {"t", "this machine's traffic"}, {"l", "logs of this app"},
			groupBreak,
			{"s", "toggle what ships with macos"}, {"/", "filter"},
		}
	}
	return []hint{
		{"enter", "launch"}, {"i", "install (os dialog)"}, {"shift+i", "install (tui picker)"}, {"ctrl+u", "uninstall"},
		groupBreak,
		{"s", "toggle preinstalled"}, {"l", "logs of this app"}, {"t", "this app's log with the device's traffic"}, {"/", "filter"},
		groupBreak,
		{"h", "home key"}, {"backspace", "back key"}, {"o", "overview key"},
	}
}

func (v *appsView) Refresh() {
	v.app.setStatus(" loading apps...")
	var apps []device.App
	v.app.async(func() error {
		var err error
		apps, err = v.app.m.Apps(v.app.ctx, v.dev)
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
		label := "preinstalled"
		if v.dev.IsHost() {
			label = "macos"
		}
		title = fmt.Sprintf(" apps @ %s [%d, +%s] ", v.dev.Name, r-1, label)
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
	switch {
	case ev.Key() == tcell.KeyEnter:
		if a, ok := v.selected(); ok {
			v.app.async(func() error { return v.app.m.LaunchApp(v.app.ctx, v.dev, a.BundleID) }, func() {
				v.app.flash("launched " + a.BundleID)
			})
		}
	case ev.Rune() == 'i', ev.Rune() == 'I':
		if v.dev.IsHost() {
			v.app.flashErr(fmt.Errorf("sims does not install apps onto the machine it runs on"))
			return nil
		}
		v.pickAndInstall(ev.Rune() == 'i')
	case ev.Key() == tcell.KeyCtrlU:
		if v.dev.IsHost() {
			v.app.flashErr(fmt.Errorf("sims does not uninstall apps from the machine it runs on"))
			return nil
		}
		if a, ok := v.selected(); ok {
			v.app.confirmDangerous(fmt.Sprintf("uninstall %s?", a.BundleID), func() {
				v.app.async(func() error { return v.app.m.UninstallApp(v.app.ctx, v.dev, a.BundleID) }, func() {
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
		v.openLogs()
	case ev.Rune() == 't':
		v.watchWithApp()
	default:
		return ev
	}
	return nil
}

// openLogs shows the selected app's log, or the device's when nothing is selected. Whether the
// platform can narrow a log to one app is settled before the view opens: a screen that appears and
// then reports it cannot do what it was opened for is worse than not opening.
func (v *appsView) openLogs() {
	a, ok := v.selected()
	if !ok {
		v.app.replaceTop(newLogsView(v.app, v.dev, nil))
		return
	}
	if err := v.app.m.CanLog(v.app.ctx, v.dev, &a); err != nil {
		v.app.flashErr(err)
		return
	}
	v.app.push(newLogsView(v.app, v.dev, &a))
}

// watchWithApp opens this app's log with the device's traffic running through it. The log is the
// app's alone; the traffic is everything the device sends, because a device's connections do not say
// which app opened them. Seeing the two together is what says whether a request followed what the
// app just logged.
func (v *appsView) watchWithApp() {
	app, ok := v.selected()
	if !ok {
		// with nothing selected there is no log to narrow, so this is the device's own mixed view
		withCapture(v.app, v.dev, func(s *capture.Session) {
			v.app.push(newTrafficView(v.app, v.dev, nil, s))
		})
		return
	}
	withCapture(v.app, v.dev, func(s *capture.Session) {
		v.app.push(newTrafficView(v.app, v.dev, &app, s))
	})
}

// The OS dialog runs off the UI goroutine; a platform without one falls back to the TUI picker.
func (v *appsView) pickAndInstall(native bool) {
	install := func(path string) {
		v.app.setStatus(" installing " + path + "...")
		v.app.async(func() error { return v.app.m.InstallApp(v.app.ctx, v.dev, path) }, func() {
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

// devicectl's install help names only .app bundles, while simctl takes a packaged .ipa as well.
func installableExts(d device.Device) []string {
	switch {
	case d.Platform == device.PlatformAndroid:
		return []string{".apk"}
	case d.Kind == device.KindVirtual:
		return []string{".app", ".ipa"}
	}
	return []string{".app"}
}

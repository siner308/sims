package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/sims"
)

type sortColumn int

const (
	colPlatform sortColumn = iota
	colVia
	colName
	colModel
	colRuntime
	colState
	colLast
	colID
)

var columnNames = [...]string{"PLATFORM", "VIA", "NAME", "MODEL", "RUNTIME", "STATE", "LAST", "ID"}

// Shift+letter picks the column like k9s; the same key again flips direction.
var sortKeys = map[rune]sortColumn{
	'P': colPlatform, 'V': colVia, 'N': colName, 'M': colModel, 'R': colRuntime, 'S': colState, 'L': colLast,
}

type sortSpec struct {
	col  sortColumn
	desc bool
}

func (c sortColumn) String() string { return columnNames[c] }

type devicesView struct {
	app     *App
	table   *tview.Table
	devices []device.Device
	filter  string
	sort    sortSpec
	now     func() time.Time
	polling bool
	// A stock Xcode install lists dozens of simulators that have never been booted (82 here, 8 with a
	// last boot); they stay out of the list until asked for.
	showUnused bool
	// focusID moves the cursor to that device on the next render (a device just created), then clears
	focusID string
}

var defaultSort = sortSpec{col: colState}

func newDevicesView(a *App) *devicesView {
	v := &devicesView{app: a, table: newTable(), now: time.Now, sort: defaultSort}
	v.table.SetTitle(" devices ")
	v.table.SetInputCapture(v.onKey)
	return v
}

func (v *devicesView) Name() string               { return "devices" }
func (v *devicesView) Primitive() tview.Primitive { return v.table }
func (v *devicesView) Hints() []hint {
	return []hint{
		{"enter", "apps (boots first)"}, {"l", "logs"}, {"b", "boot"},
		{"ctrl+k", "shutdown"}, {"ctrl+e", "wipe data (keep device)"}, {"ctrl+d", "delete device"},
		groupBreak,
		{"n", "new device"}, {"e", "edit hardware (avd)"}, {"s", "show unused sims"}, {"/", "filter"}, {"p", "pair (ios)"},
		{"w", "connect wifi"}, {"x", "disconnect wifi"},
		groupBreak,
		{"h", "home key"}, {"backspace", "back key"}, {"o", "overview key"},
		groupBreak,
		{"shift+n", "sort name"}, {"shift+s", "sort state"}, {"shift+l", "sort last"},
		{"shift+r", "sort runtime"}, {"shift+p", "sort platform"}, {"shift+v", "sort via"}, {"shift+m", "sort model"},
	}
}

func (v *devicesView) Refresh() {
	v.app.setStatus(" loading devices...")
	var all []device.Device
	var listErr error
	v.app.async(func() error {
		all, listErr = v.app.m.Devices(v.app.ctx)
		return nil
	}, func() {
		v.devices = all
		v.render()
		if listErr != nil {
			v.app.flashErr(listErr)
		} else {
			v.app.setStatus("")
		}
		v.pollTransitions()
	})
}

// A device in Booting or Shutting Down settles on its own, so the list re-reads itself until it does.
func (v *devicesView) pollTransitions() {
	if v.polling || !slices.ContainsFunc(v.devices, func(d device.Device) bool {
		return d.State == device.StateBooting || d.State == device.StateShuttingDown
	}) {
		return
	}
	v.polling = true
	time.AfterFunc(2*time.Second, func() {
		v.app.tv.QueueUpdateDraw(func() {
			v.polling = false
			if v.app.top() == v {
				v.Refresh()
			}
		})
	})
}

func compareBy(col sortColumn, a, b device.Device) int {
	switch col {
	case colPlatform:
		return strings.Compare(string(a.Platform), string(b.Platform))
	case colVia:
		return strings.Compare(string(a.Transport), string(b.Transport))
	case colName:
		return strings.Compare(a.Name, b.Name)
	case colModel:
		return strings.Compare(a.Model, b.Model)
	case colRuntime:
		return strings.Compare(a.Runtime, b.Runtime)
	case colState:
		return a.StateRank() - b.StateRank()
	case colLast:
		return a.LastActiveAt.Compare(b.LastActiveAt)
	}
	return strings.Compare(a.ID, b.ID)
}

// The default order is state, then most recent, then name desc, then runtime desc.
// A chosen column goes first and the default chain breaks its ties.
func (v *devicesView) sortDevices() {
	slices.SortStableFunc(v.devices, func(a, b device.Device) int {
		primary := compareBy(v.sort.col, a, b)
		if v.sort.desc {
			primary = -primary
		}
		return cmp.Or(primary, sims.DefaultOrder(a, b))
	})
}

func (v *devicesView) setSort(col sortColumn) {
	if v.sort.col == col {
		v.sort.desc = !v.sort.desc
	} else {
		v.sort = sortSpec{col: col, desc: col == colLast}
	}
	v.render()
}

func (v *devicesView) render() {
	v.sortDevices()
	selectedID := ""
	if d, ok := v.selected(); ok {
		selectedID = d.ID
	}
	v.table.Clear()
	headers := append([]string(nil), columnNames[:]...)
	mark := "\u2191" // ↑
	if v.sort.desc {
		mark = "\u2193" // ↓
	}
	headers[v.sort.col] += mark
	setHeader(v.table, headers...)
	r, row, hidden := 1, 1, 0
	for _, d := range v.devices {
		if !v.showUnused && d.NeverBooted() {
			hidden++
			continue
		}
		hay := strings.ToLower(d.Name + d.Model + d.Runtime + string(d.Platform) + string(d.Transport))
		if v.filter != "" && !strings.Contains(hay, strings.ToLower(v.filter)) {
			continue
		}
		if d.ID == selectedID || d.ID == v.focusID {
			row = r
		}
		if d.ID == v.focusID {
			v.focusID = ""
		}
		v.table.SetCell(r, 0, tview.NewTableCell(highlight(string(d.Platform), v.filter)).SetReference(d))
		v.table.SetCell(r, 1, tview.NewTableCell(highlight(string(d.Transport), v.filter)))
		v.table.SetCell(r, 2, tview.NewTableCell(highlight(d.Name, v.filter)))
		v.table.SetCell(r, 3, tview.NewTableCell(highlight(d.Model, v.filter)).SetTextColor(tcell.ColorGray))
		v.table.SetCell(r, 4, tview.NewTableCell(highlight(d.Runtime, v.filter)))
		v.table.SetCell(r, 5, tview.NewTableCell(stateColor(d.State)+string(d.State)+"[-]"))
		v.table.SetCell(r, 6, tview.NewTableCell(relativeTime(d.LastActiveAt, v.now())).SetTextColor(tcell.ColorGray))
		v.table.SetCell(r, 7, tview.NewTableCell(d.ID).SetTextColor(tcell.ColorGray))
		r++
	}
	if r == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell("[gray]no devices[-]").SetSelectable(false))
	}
	v.table.Select(row, 0)
	v.table.ScrollToBeginning()
	title := " devices "
	if v.filter != "" {
		title = fmt.Sprintf(" devices /%s ", v.filter)
	}
	title += fmt.Sprintf("[%d] ", r-1)
	if hidden > 0 {
		title += fmt.Sprintf("[gray]+%d unused sims (s)[-] ", hidden)
	}
	v.table.SetTitle(title)
}

func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func (v *devicesView) selected() (device.Device, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return device.Device{}, false
	}
	d, ok := cell.GetReference().(device.Device)
	return d, ok
}

func (v *devicesView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEnter:
		v.openApps()
		return nil
	case tcell.KeyCtrlK:
		v.act("shutdown", false, v.app.m.Shutdown)
		return nil
	case tcell.KeyCtrlE:
		v.act("wipe data of", true, v.app.m.Erase)
		return nil
	case tcell.KeyCtrlD:
		v.act("delete device", true, v.app.m.Delete)
		return nil
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if d, ok := v.selected(); ok {
			v.app.sendKey(d, device.KeyBack)
		}
		return nil
	}
	if col, ok := sortKeys[ev.Rune()]; ok {
		v.setSort(col)
		return nil
	}
	switch ev.Rune() {
	case '/':
		v.app.prompt("filter:", "", func(s string) { v.filter = s; v.render() })
		return nil
	case 'b':
		v.act("boot", false, v.app.m.Boot)
	case 'a':
		v.openApps()
	case 'l':
		if d, ok := v.selected(); ok {
			v.app.push(newLogsView(v.app, d, nil))
		}
	case 'n':
		v.app.push(newImagesView(v.app))
	case 'w':
		v.connect()
	case 'x':
		v.disconnect()
	case 'p':
		v.pair()
	case 'e':
		v.editHardware()
	case 's':
		v.showUnused = !v.showUnused
		v.render()
	case 'h':
		if d, ok := v.selected(); ok {
			v.app.sendKey(d, device.KeyHome)
		}
	case 'o':
		if d, ok := v.selected(); ok {
			v.app.sendKey(d, device.KeyOverview)
		}
	default:
		return ev
	}
	return nil
}

// Enter on a stopped device offers to boot it and opens apps once it reports Booted, so the
// list never opens against a device that cannot answer.
func (v *devicesView) openApps() {
	d, ok := v.selected()
	if !ok {
		return
	}
	if d.Running() {
		v.app.push(newAppsView(v.app, d))
		return
	}
	if d.Kind != device.KindVirtual {
		hint := "plug it in or connect it first"
		if d.Platform == device.PlatformIOS && d.State == device.StateOffline {
			hint = "press w to open the wifi tunnel"
		}
		if d.Platform == device.PlatformIOS && d.State == device.StateUnpaired {
			hint = "press p to pair it (USB the first time)"
		}
		v.app.flashErr(fmt.Errorf("%s is %s; %s", d.Name, strings.ToLower(string(d.State)), hint))
		return
	}
	v.app.confirm(fmt.Sprintf("%s is not running. Boot it and open apps?", d.Name), func() {
		v.app.setStatus(" booting " + d.Name + "...")
		var booted device.Device
		v.app.async(func() error {
			if err := v.app.m.Boot(v.app.ctx, d); err != nil {
				return err
			}
			var err error
			booted, err = v.app.m.WaitBooted(v.app.ctx, d, sims.BootTimeout)
			return err
		}, func() {
			v.app.setStatus("")
			v.Refresh()
			v.app.push(newAppsView(v.app, booted))
		})
	})
}

func (v *devicesView) connect() {
	d, ok := v.selected()
	if !ok {
		return
	}
	if d.Platform == device.PlatformIOS {
		v.app.setStatus(" connecting to " + d.Name + " (same wifi, unlocked, developer mode on)...")
	} else {
		v.app.setStatus(" " + d.Name + ": switching to adb over wifi...")
	}
	var note string
	v.app.async(func() error {
		var err error
		note, err = v.app.m.Connect(v.app.ctx, d)
		return err
	}, func() {
		v.app.flash(d.Name + ": " + note)
		v.Refresh()
	})
}

func (v *devicesView) disconnect() {
	d, ok := v.selected()
	if !ok {
		return
	}
	v.app.setStatus(" " + d.Name + ": disconnecting...")
	v.app.async(func() error { return v.app.m.Disconnect(v.app.ctx, d) }, func() {
		v.app.flash(d.Name + ": disconnected")
		v.Refresh()
	})
}

func (v *devicesView) editHardware() {
	d, ok := v.selected()
	if !ok {
		return
	}
	var current device.Hardware
	v.app.async(func() error {
		var err error
		current, err = v.app.m.Hardware(v.app.ctx, d)
		return err
	}, func() {
		v.app.push(newHardwareView(v.app, d, current))
	})
}

func (v *devicesView) pair() {
	d, ok := v.selected()
	if !ok {
		return
	}
	if d.Platform == device.PlatformAndroid {
		v.app.flashErr(fmt.Errorf("%s pairs with :pair HOST:PORT CODE", d.Platform))
		return
	}
	v.app.confirm(fmt.Sprintf("pair %s?\n\n%s", d.Name, pairingGuide), func() {
		v.app.setStatus(" pairing " + d.Name + ": accept the prompt on the phone (up to 2 minutes)...")
		v.app.async(func() error { return v.app.m.Pair(v.app.ctx, d) }, func() {
			v.app.flash("paired " + d.Name + "; unplug it and use w to reach it over wifi")
			v.Refresh()
		})
	})
}

// Apple's pairing steps: the first pairing goes over a cable, wifi comes after.
const pairingGuide = "Before answering yes:\n" +
	"1. Developer Mode is on (Settings > Privacy & Security > Developer Mode) and the phone is unlocked\n" +
	"2. For a phone this Mac has never seen, it is plugged in over USB and you tapped Trust\n" +
	"3. Then accept the pairing prompt that appears on the phone"

func (v *devicesView) act(verb string, dangerous bool, fn func(context.Context, device.Device) error) {
	d, ok := v.selected()
	if !ok {
		return
	}
	run := func() {
		v.app.setStatus(fmt.Sprintf(" %s %s...", verb, d.Name))
		v.app.async(func() error { return fn(v.app.ctx, d) }, func() {
			v.app.flash(fmt.Sprintf("%s %s: ok", verb, d.Name))
			v.Refresh()
		})
	}
	if dangerous {
		v.app.confirm(fmt.Sprintf("%s %s (%s)?\n\n%s", verb, d.Name, d.Platform, dangerNote(verb, d)), run)
		return
	}
	run()
}

func dangerNote(verb string, d device.Device) string {
	switch verb {
	case "wipe data of":
		return "Apps, accounts and settings are removed; the device itself stays and boots fresh."
	case "delete device":
		if d.Kind == device.KindPhysical {
			if d.Platform == device.PlatformIOS {
				return "Removes this Mac's pairing record for the phone so it leaves the list; pair again (p) to bring it back."
			}
			return "Drops the adb connection so it leaves the list. Plug it in or connect again (w) to bring it back."
		}
		return "The device and its data are removed for good. Create a new one from images (n)."
	}
	return ""
}

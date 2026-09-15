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
		{"n", "new device"}, {"/", "filter"}, {"p", "pair (ios)"},
		{"w", "wifi: android enable / ios connect"}, {"x", "disconnect wifi"},
		groupBreak,
		{"h", "home key"}, {"backspace", "back key"}, {"o", "overview key"},
		groupBreak,
		{"shift+n", "sort name"}, {"shift+s", "sort state"}, {"shift+l", "sort last"},
		{"shift+r", "sort runtime"}, {"shift+p", "sort platform"}, {"shift+v", "sort via"}, {"shift+m", "sort model"},
	}
}

func (v *devicesView) Refresh() {
	v.app.status.SetText(" loading devices...")
	var all []device.Device
	var errs []string
	v.app.async(func() error {
		for _, p := range v.app.providers {
			list, err := p.List(v.app.ctx)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			all = append(all, list...)
		}
		return nil
	}, func() {
		v.devices = all
		v.render()
		if len(errs) > 0 {
			v.app.flashErr(fmt.Errorf("%s", strings.Join(errs, "; ")))
		} else {
			v.app.status.SetText("")
		}
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
	base := func(a, b device.Device) int {
		return cmp.Or(
			compareBy(colState, a, b),
			compareBy(colLast, b, a),
			compareBy(colName, b, a),
			compareBy(colRuntime, b, a),
		)
	}
	slices.SortStableFunc(v.devices, func(a, b device.Device) int {
		primary := compareBy(v.sort.col, a, b)
		if v.sort.desc {
			primary = -primary
		}
		return cmp.Or(primary, base(a, b))
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
	row, _ := v.table.GetSelection()
	v.table.Clear()
	headers := append([]string(nil), columnNames[:]...)
	mark := "^"
	if v.sort.desc {
		mark = "v"
	}
	headers[v.sort.col] += mark
	setHeader(v.table, headers...)
	r := 1
	for _, d := range v.devices {
		hay := strings.ToLower(d.Name + d.Model + d.Runtime + string(d.Platform) + string(d.Transport))
		if v.filter != "" && !strings.Contains(hay, strings.ToLower(v.filter)) {
			continue
		}
		v.table.SetCell(r, 0, tview.NewTableCell(string(d.Platform)).SetReference(d))
		v.table.SetCell(r, 1, tview.NewTableCell(string(d.Transport)))
		v.table.SetCell(r, 2, tview.NewTableCell(d.Name))
		v.table.SetCell(r, 3, tview.NewTableCell(d.Model).SetTextColor(tcell.ColorGray))
		v.table.SetCell(r, 4, tview.NewTableCell(d.Runtime))
		v.table.SetCell(r, 5, tview.NewTableCell(stateColor(d.State)+string(d.State)+"[-]"))
		v.table.SetCell(r, 6, tview.NewTableCell(relativeTime(d.LastActiveAt, v.now())).SetTextColor(tcell.ColorGray))
		v.table.SetCell(r, 7, tview.NewTableCell(d.ID).SetTextColor(tcell.ColorGray))
		r++
	}
	if r == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell("[gray]no devices[-]").SetSelectable(false))
	}
	if row < 1 || row >= r {
		row = 1
	}
	v.table.Select(row, 0)
	title := " devices "
	if v.filter != "" {
		title = fmt.Sprintf(" devices /%s ", v.filter)
	}
	v.table.SetTitle(fmt.Sprintf("%s[%d] ", title, r-1))
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
		v.act("shutdown", false, func(p device.Provider, d device.Device) error { return p.Shutdown(v.app.ctx, d) })
		return nil
	case tcell.KeyCtrlE:
		v.act("wipe data of", true, func(p device.Provider, d device.Device) error { return p.Erase(v.app.ctx, d) })
		return nil
	case tcell.KeyCtrlD:
		v.act("delete device", true, func(p device.Provider, d device.Device) error { return p.Delete(v.app.ctx, d) })
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
		v.app.prompt("filter:", v.filter, func(s string) { v.filter = s; v.render() })
		v.filter = ""
		return nil
	case 'b':
		v.act("boot", false, func(p device.Provider, d device.Device) error { return p.Boot(v.app.ctx, d) })
	case 'a':
		v.openApps()
	case 'l':
		if d, ok := v.selected(); ok {
			v.app.push(newLogsView(v.app, d))
		}
	case 'n':
		v.app.push(newImagesView(v.app))
	case 'w':
		if d, ok := v.selected(); ok && d.Platform == device.PlatformIOS {
			v.connect(d)
			return nil
		}
		v.wireless(func(w device.Wireless, d device.Device) (string, error) { return w.EnableWireless(v.app.ctx, d) })
	case 'x':
		v.wireless(func(w device.Wireless, d device.Device) (string, error) {
			return "disconnected", w.Disconnect(v.app.ctx, d)
		})
	case 'p':
		v.pair()
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
		v.app.flashErr(fmt.Errorf("%s is %s; %s", d.Name, strings.ToLower(string(d.State)), hint))
		return
	}
	p, err := v.app.providerFor(d)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	v.app.confirm(fmt.Sprintf("%s is not running. Boot it and open apps?", d.Name), func() {
		v.app.status.SetText(" booting " + d.Name + "...")
		var booted device.Device
		v.app.async(func() error {
			if err := p.Boot(v.app.ctx, d); err != nil {
				return err
			}
			var err error
			booted, err = waitBooted(v.app.ctx, p, d, bootTimeout)
			return err
		}, func() {
			v.app.status.SetText("")
			v.Refresh()
			v.app.push(newAppsView(v.app, booted))
		})
	})
}

const bootTimeout = 3 * time.Minute

func waitBooted(ctx context.Context, p device.Provider, d device.Device, timeout time.Duration) (device.Device, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		list, err := p.List(ctx)
		if err != nil {
			return device.Device{}, err
		}
		for _, cur := range list {
			if cur.ID == d.ID && cur.Running() {
				return cur, nil
			}
		}
		select {
		case <-ctx.Done():
			return device.Device{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return device.Device{}, fmt.Errorf("%s did not finish booting within %s", d.Name, timeout)
}

func (v *devicesView) connect(d device.Device) {
	p, err := v.app.providerFor(d)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	c, ok := p.(device.Connector)
	if !ok {
		v.app.flashErr(fmt.Errorf("%s has no connect action", d.Platform))
		return
	}
	v.app.status.SetText(" connecting to " + d.Name + " (same wifi, unlocked, developer mode on)...")
	v.app.async(func() error { return c.Connect(v.app.ctx, d) }, func() {
		v.app.flash("connected " + d.Name)
		v.Refresh()
	})
}

func (v *devicesView) wireless(fn func(device.Wireless, device.Device) (string, error)) {
	d, ok := v.selected()
	if !ok {
		return
	}
	p, err := v.app.providerFor(d)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	w, ok := p.(device.Wireless)
	if !ok {
		v.app.flashErr(fmt.Errorf("%s has no wireless debugging support", d.Platform))
		return
	}
	v.app.status.SetText(" " + d.Name + ": working...")
	var result string
	v.app.async(func() error {
		var err error
		result, err = fn(w, d)
		return err
	}, func() {
		v.app.flash(d.Name + ": " + result)
		v.Refresh()
	})
}

func (v *devicesView) pair() {
	d, ok := v.selected()
	if !ok {
		return
	}
	p, err := v.app.providerFor(d)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	pairer, ok := p.(device.Pairer)
	if !ok {
		v.app.flashErr(fmt.Errorf("%s pairs with :pair HOST:PORT CODE", d.Platform))
		return
	}
	v.app.status.SetText(" pairing " + d.Name + ": accept the trust prompt on the device...")
	v.app.async(func() error { return pairer.PairDevice(v.app.ctx, d) }, func() {
		v.app.flash("paired " + d.Name)
		v.Refresh()
	})
}

func (v *devicesView) act(verb string, dangerous bool, fn func(device.Provider, device.Device) error) {
	d, ok := v.selected()
	if !ok {
		return
	}
	p, err := v.app.providerFor(d)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	run := func() {
		v.app.status.SetText(fmt.Sprintf(" %s %s...", verb, d.Name))
		v.app.async(func() error { return fn(p, d) }, func() {
			v.app.flash(fmt.Sprintf("%s %s: ok", verb, d.Name))
			v.Refresh()
		})
	}
	if dangerous {
		v.app.confirm(fmt.Sprintf("%s %s (%s)?\n\n%s", verb, d.Name, d.Platform, dangerNote(verb)), run)
		return
	}
	run()
}

func dangerNote(verb string) string {
	switch verb {
	case "wipe data of":
		return "Apps, accounts and settings are removed; the device itself stays and boots fresh."
	case "delete device":
		return "The device and its data are removed for good. Create a new one from images (n)."
	}
	return ""
}

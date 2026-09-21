package ui

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// redrawInterval batches the table rebuild: a busy app can produce hundreds of flows a second and
// the table is cheap to rebuild but not free.
const redrawInterval = 250 * time.Millisecond

type flowsView struct {
	app     *App
	dev     device.Device
	session *capture.Session
	table   *tview.Table

	flows  []proxy.Flow
	filter string
	paused bool
	// deviceOnly hides this machine's own apps. A capture only opens the device's traffic, so this
	// matters where the scope was widened to take the machine's too.
	deviceOnly bool
	// byDomain groups the table by host. A phone's connections do not say which app opened them, so
	// the domain is the handle a reader has instead.
	byDomain bool
	// collapsed holds the domains folded shut while grouped; a domain absent from it is open.
	collapsed map[string]bool
	// closed is set once the view leaves the stack, so a late Refresh does not revive its follower.
	closed bool
	stop   chan struct{}
}

func newFlowsView(a *App, d device.Device, s *capture.Session) *flowsView {
	v := &flowsView{app: a, dev: d, session: s, table: newTable(), collapsed: map[string]bool{}}
	v.table.SetInputCapture(v.onKey)
	v.table.SetTitle(v.title())
	return v
}

func (v *flowsView) Name() string               { return "proxy" }
func (v *flowsView) Primitive() tview.Primitive { return v.table }
func (v *flowsView) Hints() []hint {
	return []hint{
		{"enter", "read"}, {"v", "open the body"}, {"e", "open in an editor"},
		{"shift+e", "pick another editor"}, {"/", "filter"}, {"y", "types"}, {"c", "clear"}, {"p", "pause"},
		{"d", "device only"}, {"s", "save har"}, {"l", "mix with the log"},
		groupBreak,
		{"g", "group by domain"}, {"space", "fold a domain"}, {"shift+g", "fold or unfold all"},
		groupBreak,
		{"ctrl+k", "stop capture"}, {"esc", "back (keeps capturing)"},
	}
}

func (v *flowsView) Refresh() {
	if v.closed {
		return
	}
	v.reload()
	if v.stop == nil {
		v.watch()
	}
}

// watch redraws on a timer while the store reports changes, instead of once per flow.
func (v *flowsView) watch() {
	v.stop = make(chan struct{})
	stop, store := v.stop, v.session.Store
	// Changed reports the next change, not one already made, so a flow recorded between the caller's
	// reload and this goroutine starting would go unseen until some later flow woke it up.
	changed := store.Changed()
	go func() {
		tick := time.NewTicker(redrawInterval)
		defer tick.Stop()
		dirty := true
		for {
			select {
			case <-stop:
				return
			case <-changed:
				dirty = true
				changed = store.Changed()
			case <-tick.C:
				if !dirty {
					continue
				}
				dirty = false
				v.app.tv.QueueUpdateDraw(func() {
					if !v.paused {
						v.reload()
					}
				})
			}
		}
	}()
}

// close stops following the store. Whatever takes the view off the stack calls it, and esc calls it
// on the way out, so it has to be safe more than once.
func (v *flowsView) close() {
	v.closed = true
	if v.stop != nil {
		close(v.stop)
		v.stop = nil
	}
}

func (v *flowsView) reload() {
	v.flows = v.session.Flows()
	v.render()
}

func (v *flowsView) visible() []proxy.Flow {
	out := make([]proxy.Flow, 0, len(v.flows))
	for _, f := range v.flows {
		if v.deviceOnly && f.Origin == proxy.OriginHost {
			continue
		}
		if v.filter != "" && !strings.Contains(strings.ToLower(f.URL+f.Method+f.Process), strings.ToLower(v.filter)) {
			continue
		}
		if v.app.hiddenTypes[f.Resource()] {
			continue
		}
		out = append(out, f)
	}
	return out
}

func (v *flowsView) render() {
	if v.byDomain {
		v.renderGrouped()
		return
	}
	selectedID := int64(0)
	if f, ok := v.selected(); ok {
		selectedID = f.ID
	}
	v.table.Clear()
	setHeader(v.table, "", "WHEN", "METHOD", "STATUS", "TYPE", "HOST", "PATH", "SIZE", "TOOK", "FROM")
	row, want := 1, 1
	flows := v.visible()
	for _, f := range flows {
		if f.ID == selectedID {
			want = row
		}
		v.table.SetCell(row, 0, tview.NewTableCell(kindMark(f)).SetReference(f).SetMaxWidth(2))
		v.table.SetCell(row, 1, tview.NewTableCell(clockCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(13))
		v.table.SetCell(row, 2, tview.NewTableCell(highlight(f.Method, v.filter)).SetMaxWidth(7))
		v.table.SetCell(row, 3, tview.NewTableCell(statusCell(f)).SetMaxWidth(7))
		v.table.SetCell(row, 4, tview.NewTableCell(string(f.Resource())).SetTextColor(tcell.ColorGray).SetMaxWidth(5))
		v.table.SetCell(row, 5, tview.NewTableCell(highlight(hostOf(f), v.filter)))
		v.table.SetCell(row, 6, tview.NewTableCell(highlight(pathOf(f), v.filter)).SetExpansion(2))
		v.table.SetCell(row, 7, tview.NewTableCell(sizeCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(9))
		v.table.SetCell(row, 8, tview.NewTableCell(timeCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(8))
		v.table.SetCell(row, 9, tview.NewTableCell(fromCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(16))
		row++
	}
	if row == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell(v.emptyMessage()).SetSelectable(false))
	}
	v.table.Select(want, 0)
	v.table.SetTitle(v.title())
}

// renderGrouped draws one row per domain with its exchanges under it, so a device whose traffic
// cannot be split by app can still be read a service at a time.
func (v *flowsView) renderGrouped() {
	selectedID := int64(0)
	selectedHost := ""
	switch cur := v.currentRow().(type) {
	case proxy.Flow:
		selectedID = cur.ID
	case domainGroup:
		selectedHost = cur.Host
	}

	v.table.Clear()
	setHeader(v.table, "", "WHEN", "DOMAIN / PATH", "STATUS", "SIZE", "TOOK", "FROM")
	row, want := 1, 1
	for _, g := range groupByDomain(v.visible()) {
		mark := "\u25be" // ▾ open
		if v.collapsed[g.Host] {
			mark = "\u25b8" // ▸ folded
		}
		if g.Host == selectedHost {
			want = row
		}
		v.table.SetCell(row, 0, tview.NewTableCell("[aqua]"+mark+"[-]").SetReference(g).SetMaxWidth(2))
		v.table.SetCell(row, 1, tview.NewTableCell(relativeTime(g.Last, time.Now())).SetTextColor(tcell.ColorGray).SetMaxWidth(13))
		v.table.SetCell(row, 2, tview.NewTableCell("[::b]"+highlight(g.Host, v.filter)+"[::-]").SetExpansion(2))
		v.table.SetCell(row, 3, tview.NewTableCell(g.summary()).SetExpansion(1))
		v.table.SetCell(row, 4, tview.NewTableCell(""))
		v.table.SetCell(row, 5, tview.NewTableCell(""))
		v.table.SetCell(row, 6, tview.NewTableCell(groupSenders(g)).SetTextColor(tcell.ColorGray).SetMaxWidth(16))
		row++
		if v.collapsed[g.Host] {
			continue
		}
		for _, f := range g.Flows {
			if f.ID == selectedID {
				want = row
			}
			v.table.SetCell(row, 0, tview.NewTableCell(kindMark(f)).SetReference(f).SetMaxWidth(2))
			v.table.SetCell(row, 1, tview.NewTableCell(clockCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(13))
			v.table.SetCell(row, 2, tview.NewTableCell("  "+highlight(f.Method+" "+pathOf(f), v.filter)).SetExpansion(2))
			v.table.SetCell(row, 3, tview.NewTableCell(statusCell(f)).SetExpansion(1))
			v.table.SetCell(row, 4, tview.NewTableCell(sizeCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(9))
			v.table.SetCell(row, 5, tview.NewTableCell(timeCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(8))
			v.table.SetCell(row, 6, tview.NewTableCell(fromCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(16))
			row++
		}
	}
	if row == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell(v.emptyMessage()).SetSelectable(false))
	}
	v.table.Select(want, 0)
	v.table.SetTitle(v.title())
}

// groupSenders names the processes behind a domain's traffic when the platform lets sims see them,
// and stays empty on a device where it cannot.
func groupSenders(g domainGroup) string {
	seen := map[string]bool{}
	var names []string
	for _, f := range g.Flows {
		if f.Process == "" || seen[f.Process] {
			continue
		}
		seen[f.Process] = true
		names = append(names, f.Process)
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return fmt.Sprintf("%s +%d", names[0], len(names)-1)
}

// currentRow is whatever the cursor is on: an exchange, or a domain heading while grouped.
func (v *flowsView) currentRow() any {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return nil
	}
	return cell.GetReference()
}

// toggleGroup folds or unfolds the domain the cursor is on.
func (v *flowsView) toggleGroup() {
	g, ok := v.currentRow().(domainGroup)
	if !ok {
		return
	}
	if v.collapsed[g.Host] {
		delete(v.collapsed, g.Host)
	} else {
		v.collapsed[g.Host] = true
	}
	v.render()
}

// toggleAllGroups folds every domain, or opens every one when any is already folded.
func (v *flowsView) toggleAllGroups() {
	if !v.byDomain {
		return
	}
	if len(v.collapsed) > 0 {
		v.collapsed = map[string]bool{}
		v.render()
		return
	}
	for _, g := range groupByDomain(v.visible()) {
		v.collapsed[g.Host] = true
	}
	v.render()
}

func (v *flowsView) emptyMessage() string {
	if len(v.flows) > 0 {
		return "[gray]nothing matches; press d or / to widen[-]"
	}
	return fmt.Sprintf("[gray]waiting for traffic from %s...[-]", v.dev.Name)
}

// kindMark tells at a glance what sims could see: a readable exchange, an upgrade, or bytes it
// passed through without opening.
func kindMark(f proxy.Flow) string {
	switch f.Kind {
	case proxy.KindTunnel:
		return "[yellow]○[-]" // ○ passed through, not read
	case proxy.KindWebSocket:
		return "[aqua]⇅[-]" // ⇅
	}
	if f.Error != "" {
		return "[red]●[-]" // ●
	}
	if !f.Done {
		return "[gray]◌[-]" // ◌ still running
	}
	return "[green]●[-]"
}

func statusCell(f proxy.Flow) string {
	if f.Kind == proxy.KindTunnel {
		return "[yellow]tunnel[-]"
	}
	if f.Status == 0 {
		if f.Error != "" {
			return "[red]failed[-]"
		}
		return "[gray]...[-]"
	}
	color := "[green]"
	switch {
	case f.Status >= 500:
		color = "[red]"
	case f.Status >= 400:
		color = "[yellow]"
	case f.Status >= 300:
		color = "[aqua]"
	}
	return fmt.Sprintf("%s%d[-]", color, f.Status)
}

// hostOf is the host without the port the scheme implies. An IPv6 address is full of colons, so the
// split has to be done properly rather than by scanning for the last one: "fe80::443" is an address,
// not a host with a port.
func hostOf(f proxy.Flow) string {
	host, port, err := net.SplitHostPort(f.Host)
	if err != nil {
		return f.Host
	}
	if port == "443" || port == "80" {
		return host
	}
	return f.Host
}

func pathOf(f proxy.Flow) string {
	if f.Path == "" {
		return "-"
	}
	return f.Path
}

func sizeCell(f proxy.Flow) string {
	if f.RespSize == 0 && f.ReqSize == 0 {
		return "-"
	}
	return proxy.SizeString(f.RespSize)
}

// clockCell is when the request went out. The table used to show only how long it took, which says
// nothing about what a request happened next to.
func clockCell(f proxy.Flow) string {
	if f.Start.IsZero() {
		return "-"
	}
	return f.Start.Format("15:04:05.000")
}

func timeCell(f proxy.Flow) string {
	if !f.Done {
		return "-"
	}
	ms := f.Duration.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", f.Duration.Seconds())
}

func fromCell(f proxy.Flow) string {
	if f.Process == "" {
		return "-"
	}
	if f.Origin == proxy.OriginHost {
		return "[gray]" + f.Process + "[-]"
	}
	return f.Process
}

func (v *flowsView) title() string {
	title := fmt.Sprintf(" proxy @ %s :%d ", v.dev.Name, v.session.Port)
	flows := v.visible()
	if v.byDomain {
		title += fmt.Sprintf("[%d in %d domains] ", len(flows), len(groupByDomain(flows)))
	} else {
		title += fmt.Sprintf("[%d] ", len(flows))
	}
	if v.filter != "" {
		title += fmt.Sprintf("/%s ", v.filter)
	}
	title += v.app.typesNote()
	if v.paused {
		title += "[paused] "
	}
	if v.deviceOnly {
		title += "[gray]device only (d)[-] "
	}
	return title
}

func (v *flowsView) selected() (proxy.Flow, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return proxy.Flow{}, false
	}
	f, ok := cell.GetReference().(proxy.Flow)
	return f, ok
}

func (v *flowsView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEnter:
		if _, ok := v.currentRow().(domainGroup); ok {
			v.toggleGroup()
			return nil
		}
		if f, ok := v.selected(); ok {
			v.app.push(newReaderView(v.app, f.Method+"-"+hostOf(f), exchangeText(f)))
		}
		return nil
	case tcell.KeyCtrlK:
		v.stopCapture()
		return nil
	case tcell.KeyEscape:
		v.close()
		return ev
	}
	switch ev.Rune() {
	case 'g':
		v.byDomain = !v.byDomain
		v.app.drawHeader()
		v.render()
	case 'G':
		v.toggleAllGroups()
	case ' ':
		v.toggleGroup()
	case '/':
		v.app.prompt("filter:", v.filter, func(s string) { v.filter = s; v.render() })
	case 'y':
		v.app.push(newTypesView(v.app, func() []proxy.Flow { return v.flows }))
	case 'c':
		v.session.Store.Clear()
		v.reload()
	case 'p':
		v.paused = !v.paused
		if !v.paused {
			v.reload()
		} else {
			v.render()
		}
	case 'd':
		v.deviceOnly = !v.deviceOnly
		v.render()
	case 's':
		v.saveHAR()
	case 'l':
		v.mixWithLog()
	case 'v':
		if f, ok := v.selected(); ok {
			v.app.openBody(f)
		}
	case 'e', 'E':
		if f, ok := v.selected(); ok {
			v.app.openInEditor(f.Method+"-"+hostOf(f), exchangeText(f), ev.Rune() == 'E')
		}
	case 'r':
		v.reload()
	default:
		return ev
	}
	return nil
}

// mixWithLog swaps the table for the merged stream, where the same exchanges appear between the log
// lines around them. The capture keeps running; only the way it is shown changes.
func (v *flowsView) mixWithLog() {
	v.close()
	stream := newLogsView(v.app, v.dev, nil)
	stream.session = v.session
	v.app.replaceTop(stream)
}

func (v *flowsView) stopCapture() {
	v.app.confirm(fmt.Sprintf("stop capturing %s?\n\n%s", v.dev.Name, stopNote(v.dev)), func() {
		v.close()
		v.app.async(func() error { return v.app.m.StopCapture(v.dev) }, func() {
			v.app.flash("stopped capturing " + v.dev.Name)
			v.app.pop()
		})
	})
}

func stopNote(d device.Device) string {
	if d.IsHost() {
		return "This Mac goes back to its own network settings. The certificate stays trusted, so the next capture needs no setup."
	}
	if capture.NeedsHostProxy(d) {
		return "The simulator and this Mac go back to their own network settings. The certificate stays trusted, so the next capture needs no setup."
	}
	return "The device stops sending its traffic here. The certificate stays installed, so the next capture needs no setup."
}

func (v *flowsView) saveHAR() {
	name := fmt.Sprintf("%s-%s.har", sanitize(v.dev.Name), time.Now().Format("20060102-150405"))
	path := filepath.Join(homeDir(), name)
	v.app.prompt("save har to:", path, func(target string) {
		if target == "" {
			return
		}
		flows := v.visible()
		v.app.async(func() error { return writeHAR(target, v.app.version, flows) }, func() {
			v.app.flash(fmt.Sprintf("wrote %d flows to %s", countHAR(flows), target))
		})
	})
}

func writeHAR(path, version string, flows []proxy.Flow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := proxy.WriteHAR(f, version, flows); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// countHAR is what WriteHAR will actually write: a tunnel carries no messages to record.
func countHAR(flows []proxy.Flow) int {
	n := 0
	for _, f := range flows {
		if f.Kind != proxy.KindTunnel && f.Done {
			n++
		}
	}
	return n
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r == ':' {
			return '-'
		}
		return r
	}, s)
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

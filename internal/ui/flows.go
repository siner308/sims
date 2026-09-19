package ui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	stop       chan struct{}
}

func newFlowsView(a *App, d device.Device, s *capture.Session) *flowsView {
	v := &flowsView{app: a, dev: d, session: s, table: newTable()}
	v.table.SetInputCapture(v.onKey)
	v.table.SetTitle(v.title())
	return v
}

func (v *flowsView) Name() string               { return "proxy" }
func (v *flowsView) Primitive() tview.Primitive { return v.table }
func (v *flowsView) Hints() []hint {
	return []hint{
		{"enter", "inspect"}, {"/", "filter"}, {"c", "clear"}, {"p", "pause"},
		{"d", "device only"}, {"s", "save har"}, {"l", "mix with the log"},
		groupBreak,
		{"ctrl+k", "stop capture"}, {"esc", "back (keeps capturing)"},
	}
}

// needsHostProxy mirrors the capture package: only an iOS simulator borrows this machine's settings.
func needsHostProxy(d device.Device) bool {
	return d.Platform == device.PlatformIOS && d.Kind == device.KindVirtual
}

func (v *flowsView) Refresh() {
	v.reload()
	if v.stop == nil {
		v.watch()
	}
}

// watch redraws on a timer while the store reports changes, instead of once per flow.
func (v *flowsView) watch() {
	v.stop = make(chan struct{})
	stop := v.stop
	go func() {
		tick := time.NewTicker(redrawInterval)
		defer tick.Stop()
		dirty := false
		changed := v.session.Store.Changed()
		for {
			select {
			case <-stop:
				return
			case <-changed:
				dirty = true
				changed = v.session.Store.Changed()
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
		out = append(out, f)
	}
	return out
}

func (v *flowsView) render() {
	selectedID := int64(0)
	if f, ok := v.selected(); ok {
		selectedID = f.ID
	}
	v.table.Clear()
	setHeader(v.table, "", "METHOD", "STATUS", "HOST", "PATH", "SIZE", "TIME", "FROM")
	row, want := 1, 1
	flows := v.visible()
	for _, f := range flows {
		if f.ID == selectedID {
			want = row
		}
		v.table.SetCell(row, 0, tview.NewTableCell(kindMark(f)).SetReference(f).SetMaxWidth(2))
		v.table.SetCell(row, 1, tview.NewTableCell(highlight(f.Method, v.filter)).SetMaxWidth(7))
		v.table.SetCell(row, 2, tview.NewTableCell(statusCell(f)).SetMaxWidth(7))
		v.table.SetCell(row, 3, tview.NewTableCell(highlight(hostOf(f), v.filter)))
		v.table.SetCell(row, 4, tview.NewTableCell(highlight(pathOf(f), v.filter)).SetExpansion(2))
		v.table.SetCell(row, 5, tview.NewTableCell(sizeCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(9))
		v.table.SetCell(row, 6, tview.NewTableCell(timeCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(8))
		v.table.SetCell(row, 7, tview.NewTableCell(fromCell(f)).SetTextColor(tcell.ColorGray).SetMaxWidth(16))
		row++
	}
	if row == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell(v.emptyMessage()).SetSelectable(false))
	}
	v.table.Select(want, 0)
	v.table.SetTitle(v.title())
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

func hostOf(f proxy.Flow) string {
	host := f.Host
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		if host[i+1:] == "443" || host[i+1:] == "80" {
			host = host[:i]
		}
	}
	return host
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
	title += fmt.Sprintf("[%d] ", len(v.visible()))
	if v.filter != "" {
		title += fmt.Sprintf("/%s ", v.filter)
	}
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
		if f, ok := v.selected(); ok {
			v.app.push(newFlowView(v.app, v.session, f.ID))
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
	case '/':
		v.app.prompt("filter:", v.filter, func(s string) { v.filter = s; v.render() })
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
	logs := newLogsView(v.app, v.dev, nil)
	logs.session = v.session
	v.app.replaceTop(logs)
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
	if needsHostProxy(d) {
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

// flowView is one exchange in full: what was sent, what came back, and what sims could not read.
type flowView struct {
	app     *App
	session *capture.Session
	id      int64
	text    *tview.TextView
	tab     int
}

var flowTabs = []string{"overview", "request", "response"}

func newFlowView(a *App, s *capture.Session, id int64) *flowView {
	v := &flowView{app: a, session: s, id: id}
	v.text = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	v.text.SetBorder(true)
	v.text.SetInputCapture(v.onKey)
	return v
}

func (v *flowView) Name() string               { return "flow" }
func (v *flowView) Primitive() tview.Primitive { return v.text }
func (v *flowView) Hints() []hint {
	return []hint{{"tab", "next section"}, {"shift+tab", "previous"}, {"g", "top"}, {"shift+g", "bottom"}, {"esc", "back"}}
}

func (v *flowView) Refresh() {
	f, ok := v.session.Store.Get(v.id)
	if !ok {
		v.text.SetText(" [gray]this flow has scrolled out of the buffer[-]")
		return
	}
	v.text.SetTitle(fmt.Sprintf(" %s %s [%s] ", f.Method, hostOf(f), flowTabs[v.tab]))
	v.text.SetText(v.body(f))
	v.text.ScrollToBeginning()
}

func (v *flowView) body(f proxy.Flow) string {
	var b strings.Builder
	switch flowTabs[v.tab] {
	case "overview":
		writeOverview(&b, f)
	case "request":
		writeHeaders(&b, f.ReqHeader)
		writeBody(&b, f.ReqHeader, f.ReqBody, f.ReqSize, f.ReqTruncated)
	case "response":
		if f.Kind == proxy.KindTunnel {
			fmt.Fprintf(&b, "\n [yellow]sims did not read this exchange.[-]\n\n %s\n", tview.Escape(f.Error))
			break
		}
		writeHeaders(&b, f.RespHeader)
		writeBody(&b, f.RespHeader, f.RespBody, f.RespSize, f.RespTruncated)
	}
	return b.String()
}

func writeOverview(b *strings.Builder, f proxy.Flow) {
	row := func(k, val string) {
		if val == "" {
			return
		}
		fmt.Fprintf(b, " [aqua]%-12s[-] %s\n", k, tview.Escape(val))
	}
	fmt.Fprintln(b)
	row("url", f.URL)
	row("method", f.Method)
	if f.Status > 0 {
		row("status", fmt.Sprintf("%d %s", f.Status, f.StatusText))
	}
	row("kind", string(f.Kind))
	row("from", f.Process)
	row("client", f.Client)
	row("started", f.Start.Format("15:04:05.000"))
	if f.Done {
		row("took", f.Duration.String())
	} else {
		row("took", "still running")
	}
	row("sent", proxy.SizeString(f.ReqSize))
	row("received", proxy.SizeString(f.RespSize))
	if f.Error != "" {
		fmt.Fprintf(b, "\n [red]%s[-]\n", tview.Escape(f.Error))
	}
	if f.Kind == proxy.KindTunnel {
		fmt.Fprint(b, "\n [gray]The bytes went through untouched, so there is nothing to show. An app that pins its\n"+
			" certificate refuses any proxy, including this one.[-]\n")
	}
}

func writeHeaders(b *strings.Builder, h http.Header) {
	if len(h) == 0 {
		fmt.Fprintln(b, "\n [gray]no headers[-]")
		return
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(b)
	for _, k := range keys {
		for _, val := range h[k] {
			fmt.Fprintf(b, " [aqua]%s:[-] %s\n", tview.Escape(k), tview.Escape(val))
		}
	}
}

func writeBody(b *strings.Builder, h http.Header, body []byte, size int64, truncated bool) {
	if size == 0 {
		fmt.Fprintln(b, "\n [gray]no body[-]")
		return
	}
	fmt.Fprintf(b, "\n [gray]--- body (%s) ---[-]\n\n", proxy.SizeString(size))
	fmt.Fprintln(b, tview.Escape(proxy.Pretty(h, body)))
	if truncated {
		fmt.Fprintf(b, "\n [gray]...the rest was not kept; sims holds the first %s of each body[-]\n", proxy.SizeString(int64(len(body))))
	}
}

func (v *flowView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyTab:
		v.tab = (v.tab + 1) % len(flowTabs)
		v.Refresh()
		return nil
	case tcell.KeyBacktab:
		v.tab = (v.tab - 1 + len(flowTabs)) % len(flowTabs)
		v.Refresh()
		return nil
	}
	switch ev.Rune() {
	case 'g':
		v.text.ScrollToBeginning()
	case 'G':
		v.text.ScrollToEnd()
	case 'r':
		v.Refresh()
	default:
		return ev
	}
	return nil
}

package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/capture"
	"github.com/siner308/sims/internal/device"
	"github.com/siner308/sims/internal/proxy"
)

// cursorMark sits in front of the selected exchange, so which row the keys act on is visible even
// where a terminal renders the region highlight faintly.
const cursorMark = "[dodgerblue]\u25b8[-]"

const (
	logBuffer    = 5000
	pumpInterval = 100 * time.Millisecond
)

type logsView struct {
	app    *App
	dev    device.Device
	only   *device.App
	text   *tview.TextView
	cancel context.CancelFunc
	mu     sync.Mutex
	lines  []string
	filter string
	paused bool
	nowrap bool
	// noFollow stops the view jumping to the end as lines arrive, even while it is sitting there.
	noFollow bool
	// waiting is set while the runner shows in the status bar; only the UI goroutine touches it
	waiting bool

	// timeline holds both streams in time order while traffic is mixed in; nil means logs alone.
	timeline *timeline
	session  *capture.Session
	watchOff chan struct{}
	// cursor is the id of the exchange the reader has stepped to, empty when none is selected.
	cursor string
	// warnedDeviceWide keeps the "traffic is the whole device" notice to once per view.
	warnedDeviceWide bool
	// closed is set once the view leaves the stack, so a late Refresh does not revive it.
	closed bool
	// logOff turns the log layer off, leaving the traffic. The two are layers of one stream rather
	// than two screens, so either can be dropped without leaving the other.
	logOff bool
	// opened says how much of each exchange is shown; the cursor's own level is kept separately so
	// opening one does not open every one.
	opened map[string]detail
}

// newTrafficView opens the same stream on its traffic layer, with the log off until l adds it.
func newTrafficView(a *App, d device.Device, only *device.App, s *capture.Session) *logsView {
	v := newLogsView(a, d, only)
	v.session = s
	v.logOff = true
	return v
}

func newLogsView(a *App, d device.Device, only *device.App) *logsView {
	v := &logsView{app: a, dev: d, only: only, opened: map[string]detail{}}
	v.text = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetMaxLines(logBuffer)
	// regions mark each exchange so n and N can step between them and o can open the one selected
	v.text.SetRegions(true)
	v.text.SetBorder(true).SetTitle(v.title())
	v.text.SetInputCapture(v.onKey)
	return v
}

// mixing reports whether the device's traffic layer is on.
func (v *logsView) mixing() bool { return v.session != nil }

// showingLog reports whether the log layer is on. Both layers are the same stream, and each is
// toggled on its own: t adds or removes the traffic, l the log.
func (v *logsView) showingLog() bool { return !v.logOff }

func (v *logsView) Name() string {
	switch {
	case v.mixing() && v.showingLog():
		return "logs+traffic"
	case v.mixing():
		return "traffic"
	}
	return "logs"
}

// pageKey stays put while Name changes with t, so the page is removed under the key it was added
// under rather than left registered for good.
func (v *logsView) pageKey() string { return "logs" }

func (v *logsView) Primitive() tview.Primitive { return v.text }

func (v *logsView) Hints() []hint {
	hints := []hint{
		{"/", "filter"}, {"c", "clear"}, {"p", "pause"}, {"f", v.followHint()}, {"w", "toggle wrap"},
		{"g", "top"}, {"shift+g", "bottom"},
	}
	layers := []hint{{"t", v.layerHint(v.mixing(), "traffic")}, {"l", v.layerHint(v.showingLog(), "log")}}
	if !v.mixing() {
		return append(append(hints, groupBreak), layers...)
	}
	return append(append(hints, groupBreak,
		hint{"up/down", "step exchanges"}, hint{"o", "open headers, then body"},
		hint{"O", "open every exchange"}, hint{"enter", "read"}, hint{"v", "open the body"},
		hint{"e", "open in an editor"}, hint{"shift+e", "pick another editor"},
		groupBreak),
		append(layers, hint{"ctrl+k", "stop capture"})...)
}

// followHint says what f does next rather than what the view is doing, the way the layer keys do.
func (v *logsView) followHint() string {
	if v.noFollow {
		return "follow new lines"
	}
	return "hold the view still"
}

func (v *logsView) layerHint(on bool, what string) string {
	if on {
		return "hide " + what
	}
	return "show " + what
}

// toggleLog turns the log layer off or on. With the traffic off too there is nothing to show, so
// the last layer stays.
func (v *logsView) toggleLog() {
	if v.showingLog() && !v.mixing() {
		v.app.flashErr(fmt.Errorf("this is the log; press t to add %s's traffic to it", v.dev.Name))
		return
	}
	v.logOff = v.showingLog()
	v.app.drawHeader()
	if v.showingLog() {
		// the stream was traffic only, so the log has to start flowing again
		v.Refresh()
		return
	}
	v.stop()
	v.redraw()
}

// close stops the log stream and the traffic follower; whatever takes the view off the stack calls
// it. A closed view stays closed: a Refresh queued before it left the stack would otherwise start
// both goroutines again, on a page nobody can see, for the life of the process.
func (v *logsView) close() {
	v.closed = true
	v.stop()
	v.unwatchTraffic()
}

// toggleTraffic mixes the device's exchanges into the log, or takes them back out. Turning it on
// starts a capture when none is running, which changes settings on the device, so it asks first.
func (v *logsView) toggleTraffic() {
	if v.mixing() {
		v.unwatchTraffic()
		v.session = nil
		v.timeline = nil
		v.app.drawHeader()
		v.Refresh()
		return
	}
	withCapture(v.app, v.dev, func(s *capture.Session) {
		v.session = s
		v.app.drawHeader()
		v.Refresh()
		v.warnTrafficIsDeviceWide()
	})
}

// warnTrafficIsDeviceWide is shown once per mixed view scoped to an app. Without it the app's name
// in the title reads as if every request below came from that app, and on a device none of them is
// knowably its own.
func (v *logsView) warnTrafficIsDeviceWide() {
	if v.only == nil || v.warnedDeviceWide {
		return
	}
	v.warnedDeviceWide = true
	v.app.flash(fmt.Sprintf("the log is %s's; the traffic is everything %s sends", v.only.Name, v.dev.Name))
}

// watchTraffic follows the capture's store on a timer, the same way the flows view does: a busy app
// changes it hundreds of times a second and the text is rebuilt on each redraw.
func (v *logsView) watchTraffic() {
	v.unwatchTraffic()
	v.watchOff = make(chan struct{})
	off, session := v.watchOff, v.session
	// the session is read here and never again from the goroutine below: the view's own fields
	// belong to the UI goroutine, and the callback runs there
	store := session.Store
	// subscribing before the goroutine starts: Changed reports the next change, not one already made
	changed := store.Changed()
	go func() {
		tick := time.NewTicker(redrawInterval)
		defer tick.Stop()
		dirty := true
		for {
			select {
			case <-off:
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
					// by the time this runs the traffic may have been switched off
					// the traffic layer may have gone off between the tick and this callback
					if v.session != session || v.timeline == nil || v.paused {
						return
					}
					v.timeline.setFlows(session.Flows())
					v.redraw()
				})
			}
		}
	}()
}

func (v *logsView) unwatchTraffic() {
	if v.watchOff != nil {
		close(v.watchOff)
		v.watchOff = nil
	}
}

// exchanges are the entries in the stream that are requests or responses, in the order shown.
func (v *logsView) exchanges() []entry {
	if v.timeline == nil {
		return nil
	}
	var out []entry
	for _, e := range v.visibleEntries() {
		if e.kind != entryLog {
			out = append(out, e)
		}
	}
	return out
}

// entryID names one row so a region can point at it. A log line has no id: only exchanges are
// stepped between and opened.
func entryID(e entry) string {
	if e.kind == entryLog {
		return ""
	}
	return fmt.Sprintf("ex-%d", e.flow.ID)
}

// selectedFlow is the exchange the cursor is on.
func (v *logsView) selectedFlow() (proxy.Flow, bool) {
	if !v.mixing() || v.cursor == "" {
		return proxy.Flow{}, false
	}
	for _, e := range v.exchanges() {
		if entryID(e) == v.cursor {
			return e.flow, true
		}
	}
	return proxy.Flow{}, false
}

// step moves the cursor to the next exchange in the stream, or the previous one. With no cursor yet
// it starts at the last exchange, which is the one the reader is watching arrive.
func (v *logsView) step(forward bool) {
	if !v.mixing() {
		return
	}
	ex := v.exchanges()
	if len(ex) == 0 {
		v.app.flash("no exchanges yet")
		return
	}
	at := -1
	for i, e := range ex {
		if entryID(e) == v.cursor {
			at = i
			break
		}
	}
	switch {
	case at < 0:
		// nothing selected yet: start at the newest exchange, which is the one arriving now
		at = len(ex) - 1
	case forward:
		at = min(at+1, len(ex)-1)
	default:
		at = max(at-1, 0)
	}
	v.cursor = entryID(ex[at])
	v.redraw()
	v.text.Highlight(v.cursor)
	v.text.ScrollToHighlight()
}

// openMore shows more of the selected exchange in place: its headers, then its body, then back to
// the one-line form.
func (v *logsView) openMore() {
	if !v.mixing() {
		return
	}
	if v.cursor == "" {
		v.step(true)
		if v.cursor == "" {
			return
		}
	}
	next := v.opened[v.cursor].next()
	if next == detailLine {
		delete(v.opened, v.cursor)
	} else {
		v.opened[v.cursor] = next
	}
	v.redraw()
	v.text.Highlight(v.cursor)
	v.text.ScrollToHighlight()
}

// readSelected opens the exchange in full. editor sends it straight out to $EDITOR; otherwise it
// opens on a page of its own inside sims, which esc closes, and enter there hands the same text to
// $PAGER for folding and copying out.
func (v *logsView) readSelected(editor, pick bool) {
	if !v.mixing() {
		return
	}
	if v.cursor == "" {
		v.step(true)
	}
	f, ok := v.selectedFlow()
	if !ok {
		v.app.flash("no exchange selected; press n first")
		return
	}
	title, body := f.Method+"-"+hostOf(f), exchangeText(f)
	if editor {
		v.app.openInEditor(title, body, pick)
		return
	}
	v.app.push(newReaderView(v.app, title, body))
}

// openAll opens every exchange at once, for reading a whole conversation rather than one call.
func (v *logsView) openAll() {
	if !v.mixing() {
		return
	}
	ex := v.exchanges()
	if len(ex) == 0 {
		return
	}
	// if anything is open, closing everything is what the key should do
	if len(v.opened) > 0 {
		v.opened = map[string]detail{}
		v.app.flash("closed every exchange")
	} else {
		for _, e := range ex {
			v.opened[entryID(e)] = detailBody
		}
		v.app.flash(fmt.Sprintf("opened %d exchanges", len(ex)))
	}
	v.redraw()
}

func (v *logsView) visibleEntries() []entry {
	// the timeline only exists while the traffic layer is on; with it off the stream is plain log
	// lines and there is nothing here to show
	if v.timeline == nil {
		return nil
	}
	var out []entry
	for _, e := range v.timeline.all() {
		if e.kind == entryLog && !v.showingLog() {
			continue
		}
		if !e.matches(v.filter) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (v *logsView) Refresh() {
	if v.closed {
		return
	}
	if !v.showingLog() {
		// traffic only: no log process to start, but the timeline still needs the exchanges
		if v.mixing() {
			if v.timeline == nil {
				v.timeline = newTimeline(logBuffer)
			}
			v.timeline.setFlows(v.session.Flows())
			v.watchTraffic()
			v.redraw()
		}
		return
	}
	if v.mixing() {
		if v.timeline == nil {
			v.timeline = newTimeline(logBuffer)
			// whatever the capture already collected belongs on the timeline too
			v.timeline.setFlows(v.session.Flows())
		}
		v.watchTraffic()
	}
	v.stop()
	ctx, cancel := context.WithCancel(v.app.ctx)
	v.cancel = cancel
	cmd, err := v.app.m.LogCmd(ctx, v.dev, v.only)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	if cmd == nil {
		// a provider with no log stream for this device: the view still works for whatever else it
		// shows, so this is a note rather than a crash
		v.app.flash("no log stream for " + v.dev.Name)
		return
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		v.app.flashErr(err)
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		v.app.flashErr(err)
		return
	}
	v.text.Clear()
	v.text.ScrollToEnd()
	v.waiting = true
	v.app.startSpinner(v.waitMsg())
	go v.pump(out)
	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		v.app.tv.QueueUpdateDraw(func() {
			v.settle()
			if err != nil {
				v.app.flashErr(fmt.Errorf("log stream ended: %w", err))
			} else {
				v.app.flash("log stream ended")
			}
		})
	}()
}

func (v *logsView) waitMsg() string {
	if v.only != nil {
		return fmt.Sprintf("waiting for %s to log something on %s (nothing yet since the stream opened)", v.only.Name, v.dev.Name)
	}
	return fmt.Sprintf("waiting for the first log line from %s", v.dev.Name)
}

// settle takes the runner down once the stream produced something or went away.
func (v *logsView) settle() {
	if v.waiting {
		v.waiting = false
		v.app.stopSpinner()
	}
}

func (v *logsView) stop() {
	v.settle()
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
}

// pump batches lines so a backlog does not redraw per line, but flushes on a timer too:
// one app's logs may trickle in a line at a time and each must show right away.
func (v *logsView) pump(r io.Reader) {
	lines := make(chan string, 1024)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	tick := time.NewTicker(pumpInterval)
	defer tick.Stop()
	batch := make([]string, 0, 256)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		chunk := append([]string(nil), batch...)
		batch = batch[:0]
		v.app.tv.QueueUpdateDraw(func() { v.append(chunk) })
	}
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				flush()
				return
			}
			batch = append(batch, l)
			if len(batch) >= 256 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (v *logsView) append(chunk []string) {
	v.settle()
	v.mu.Lock()
	v.lines = append(v.lines, chunk...)
	if len(v.lines) > logBuffer {
		v.lines = v.lines[len(v.lines)-logBuffer:]
	}
	v.mu.Unlock()
	if v.mixing() && v.timeline != nil {
		now := time.Now()
		for _, l := range chunk {
			v.timeline.addLog(l, now)
		}
		if !v.paused {
			// a new line can land before an exchange already on the timeline, so the whole text is rebuilt
			v.redraw()
		}
		return
	}
	if v.paused {
		return
	}
	for _, l := range chunk {
		if v.filter == "" || strings.Contains(strings.ToLower(l), strings.ToLower(v.filter)) {
			fmt.Fprintln(v.text, highlight(l, v.filter))
		}
	}
}

// redraw rebuilds the text but keeps the reader's place: a view scrolled up stays where it was,
// only a view that was already at the end keeps following new lines.
func (v *logsView) redraw() {
	row, _ := v.text.GetScrollOffset()
	_, _, _, height := v.text.GetInnerRect()
	atEnd := !v.noFollow && row+height >= v.text.GetOriginalLineCount()
	v.text.Clear()
	if v.mixing() {
		for _, e := range v.visibleEntries() {
			id := entryID(e)
			if id == "" {
				fmt.Fprintln(v.text, e.render(v.filter, detailLine))
				continue
			}
			text := e.render(v.filter, v.opened[id])
			if id == v.cursor {
				text = cursorMark + text
			}
			// the region wraps the whole entry so ScrollToHighlight lands on its first line
			fmt.Fprintf(v.text, "[\"%s\"]%s[\"\"]\n", id, text)
		}
	} else {
		v.mu.Lock()
		lines := append([]string(nil), v.lines...)
		v.mu.Unlock()
		for _, l := range lines {
			if v.filter == "" || strings.Contains(strings.ToLower(l), strings.ToLower(v.filter)) {
				fmt.Fprintln(v.text, highlight(l, v.filter))
			}
		}
	}
	if atEnd {
		v.text.ScrollToEnd()
	} else {
		v.text.ScrollTo(row, 0)
	}
	if v.cursor != "" {
		v.text.Highlight(v.cursor)
	}
	title := v.title()
	if v.filter != "" {
		title += fmt.Sprintf("/%s ", v.filter)
	}
	if v.paused {
		title += "[paused] "
	}
	if v.noFollow {
		title += "[held] "
	}
	if v.nowrap {
		title += "[nowrap] "
	}
	v.text.SetTitle(title)
}

func (v *logsView) title() string {
	if !v.mixing() {
		if v.only != nil {
			return fmt.Sprintf(" logs @ %s / %s ", v.dev.Name, v.only.Name)
		}
		return fmt.Sprintf(" logs @ %s ", v.dev.Name)
	}
	if !v.showingLog() {
		return fmt.Sprintf(" traffic @ %s :%d ", v.dev.Name, v.session.Port)
	}
	// the log can be one app's, the traffic never is: a device's connections do not say which app
	// opened them, so the title says whose each half is rather than putting one name over both
	if v.only != nil {
		return fmt.Sprintf(" logs of %s + traffic of %s :%d ", v.only.Name, v.dev.Name, v.session.Port)
	}
	return fmt.Sprintf(" logs+traffic @ %s :%d ", v.dev.Name, v.session.Port)
}

func (v *logsView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Rune() {
	case '/':
		v.app.prompt("filter:", "", func(s string) { v.filter = s; v.redraw() })
	case 'c':
		v.mu.Lock()
		v.lines = nil
		v.mu.Unlock()
		if v.mixing() && v.timeline != nil {
			v.timeline.clear()
			v.session.Store.Clear()
			// the exchanges are gone, so what was opened and where the cursor sat are too
			v.opened = map[string]detail{}
			v.cursor = ""
		}
		v.text.Clear()
	case 'p':
		v.paused = !v.paused
		v.redraw()
	case 'f':
		v.noFollow = !v.noFollow
		if !v.noFollow {
			v.text.ScrollToEnd()
		}
		v.redraw()
	case 'v':
		if v.mixing() {
			v.viewBody()
		}
	case 'w':
		v.nowrap = !v.nowrap
		v.text.SetWrap(!v.nowrap)
		v.redraw()
	case 'g':
		v.text.ScrollToBeginning()
	case 'G':
		v.text.ScrollToEnd()
	case 'r':
		v.Refresh()
	case 't':
		v.toggleTraffic()
	case 'l':
		v.toggleLog()
	case 'n':
		v.step(true)
	case 'N':
		v.step(false)
	case 'o':
		v.openMore()
	case 'O':
		v.openAll()
	case 'e', 'E':
		v.readSelected(true, ev.Rune() == 'E')
	default:
		switch ev.Key() {
		case tcell.KeyDown:
			// the arrows step exchanges while there are any; with the traffic off they scroll, which
			// is all a plain log has to move through
			if v.mixing() {
				v.step(true)
				return nil
			}
		case tcell.KeyUp:
			if v.mixing() {
				v.step(false)
				return nil
			}
		case tcell.KeyEnter:
			if v.mixing() {
				v.readSelected(false, false)
				return nil
			}
		case tcell.KeyCtrlK:
			if v.mixing() {
				v.stopCapture()
				return nil
			}
		case tcell.KeyEscape:
			v.stop()
		}
		return ev
	}
	return nil
}

// stopCapture ends the capture and leaves the log running on its own.
func (v *logsView) stopCapture() {
	d := v.dev
	v.app.confirm(fmt.Sprintf("stop capturing %s?\n\n%s", d.Name, stopNote(d)), func() {
		v.unwatchTraffic()
		v.session, v.timeline = nil, nil
		v.app.async(func() error { return v.app.m.StopCapture(d) }, func() {
			v.app.flash("stopped capturing " + d.Name + "; the log keeps running")
			v.app.drawHeader()
			v.Refresh()
		})
	})
}

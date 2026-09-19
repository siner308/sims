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
	// waiting is set while the runner shows in the status bar; only the UI goroutine touches it
	waiting bool

	// timeline holds both streams in time order while traffic is mixed in; nil means logs alone.
	timeline *timeline
	session  *capture.Session
	watchOff chan struct{}
}

func newLogsView(a *App, d device.Device, only *device.App) *logsView {
	v := &logsView{app: a, dev: d, only: only}
	v.text = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetMaxLines(logBuffer)
	v.text.SetBorder(true).SetTitle(v.title())
	v.text.SetInputCapture(v.onKey)
	return v
}

// mixing reports whether the device's traffic is being shown alongside the log.
func (v *logsView) mixing() bool { return v.session != nil }

func (v *logsView) Name() string {
	if v.mixing() {
		return "logs+traffic"
	}
	return "logs"
}

func (v *logsView) Primitive() tview.Primitive { return v.text }

func (v *logsView) Hints() []hint {
	hints := []hint{{"/", "filter"}, {"c", "clear"}, {"p", "pause"}, {"w", "toggle wrap"}, {"g", "top"}, {"shift+g", "bottom"}}
	if v.mixing() {
		return append(hints, groupBreak, hint{"t", "hide traffic"}, hint{"enter", "inspect request"}, hint{"ctrl+k", "stop capture"})
	}
	return append(hints, groupBreak, hint{"t", "mix in traffic"})
}

// close stops the log stream and the traffic follower; whatever takes the view off the stack calls it.
func (v *logsView) close() {
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
	})
}

// watchTraffic follows the capture's store on a timer, the same way the flows view does: a busy app
// changes it hundreds of times a second and the text is rebuilt on each redraw.
func (v *logsView) watchTraffic() {
	v.unwatchTraffic()
	v.watchOff = make(chan struct{})
	off, session := v.watchOff, v.session
	go func() {
		// the session is read here and never again from this goroutine: the view's own fields belong
		// to the UI goroutine, and the callback below runs there
		store := session.Store
		tick := time.NewTicker(redrawInterval)
		defer tick.Stop()
		changed := store.Changed()
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
					if v.session != session || v.paused {
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

// selectedFlow is the exchange on the line the cursor sits on, for enter to open.
func (v *logsView) selectedFlow() (proxy.Flow, bool) {
	if !v.mixing() {
		return proxy.Flow{}, false
	}
	row, _ := v.text.GetScrollOffset()
	_, _, _, height := v.text.GetInnerRect()
	visible := v.visibleEntries()
	// the last exchange on screen is the one the reader is looking at
	end := min(row+height, len(visible))
	for i := end - 1; i >= 0 && i >= row; i-- {
		if visible[i].kind != entryLog {
			return visible[i].flow, true
		}
	}
	return proxy.Flow{}, false
}

func (v *logsView) visibleEntries() []entry {
	var out []entry
	for _, e := range v.timeline.all() {
		if e.matches(v.filter) {
			out = append(out, e)
		}
	}
	return out
}

func (v *logsView) Refresh() {
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
	if v.mixing() {
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
	atEnd := row+height >= v.text.GetOriginalLineCount()
	v.text.Clear()
	if v.mixing() {
		for _, e := range v.visibleEntries() {
			fmt.Fprintln(v.text, e.render(v.filter))
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
	title := v.title()
	if v.filter != "" {
		title += fmt.Sprintf("/%s ", v.filter)
	}
	if v.paused {
		title += "[paused] "
	}
	if v.nowrap {
		title += "[nowrap] "
	}
	v.text.SetTitle(title)
}

func (v *logsView) title() string {
	what := "logs"
	if v.mixing() {
		what = fmt.Sprintf("logs+traffic :%d", v.session.Port)
	}
	if v.only != nil {
		return fmt.Sprintf(" %s @ %s / %s ", what, v.dev.Name, v.only.Name)
	}
	return fmt.Sprintf(" %s @ %s ", what, v.dev.Name)
}

func (v *logsView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Rune() {
	case '/':
		v.app.prompt("filter:", "", func(s string) { v.filter = s; v.redraw() })
	case 'c':
		v.mu.Lock()
		v.lines = nil
		v.mu.Unlock()
		if v.mixing() {
			v.timeline.clear()
			v.session.Store.Clear()
		}
		v.text.Clear()
	case 'p':
		v.paused = !v.paused
		v.redraw()
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
	default:
		switch ev.Key() {
		case tcell.KeyEnter:
			if f, ok := v.selectedFlow(); ok {
				v.app.push(newFlowView(v.app, v.session, f.ID))
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

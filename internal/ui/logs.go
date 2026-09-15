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

	"github.com/siner308/sims/internal/device"
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
}

func newLogsView(a *App, d device.Device, only *device.App) *logsView {
	v := &logsView{app: a, dev: d, only: only}
	v.text = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetMaxLines(logBuffer)
	v.text.SetBorder(true).SetTitle(v.title())
	v.text.SetInputCapture(v.onKey)
	return v
}

func (v *logsView) Name() string               { return "logs" }
func (v *logsView) Primitive() tview.Primitive { return v.text }
func (v *logsView) Hints() []hint {
	return []hint{{"/", "filter"}, {"c", "clear"}, {"p", "pause"}, {"w", "toggle wrap"}, {"g", "top"}, {"shift+g", "bottom"}}
}

func (v *logsView) Refresh() {
	v.stop()
	p, err := v.app.providerFor(v.dev)
	if err != nil {
		v.app.flashErr(err)
		return
	}
	ctx, cancel := context.WithCancel(v.app.ctx)
	v.cancel = cancel
	cmd, err := p.LogCmd(ctx, v.dev, v.only)
	if err != nil {
		v.app.flashErr(err)
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
	v.mu.Lock()
	lines := append([]string(nil), v.lines...)
	v.mu.Unlock()
	for _, l := range lines {
		if v.filter == "" || strings.Contains(strings.ToLower(l), strings.ToLower(v.filter)) {
			fmt.Fprintln(v.text, highlight(l, v.filter))
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
	if v.only != nil {
		return fmt.Sprintf(" logs @ %s / %s ", v.dev.Name, v.only.Name)
	}
	return fmt.Sprintf(" logs @ %s ", v.dev.Name)
}

func (v *logsView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Rune() {
	case '/':
		v.app.prompt("filter:", "", func(s string) { v.filter = s; v.redraw() })
	case 'c':
		v.mu.Lock()
		v.lines = nil
		v.mu.Unlock()
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
	default:
		if ev.Key() == tcell.KeyEscape {
			v.stop()
		}
		return ev
	}
	return nil
}

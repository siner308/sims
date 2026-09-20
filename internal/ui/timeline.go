package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/proxy"
)

// detail is how much of an exchange the stream shows in place. The list is ordered, so a key can
// step through it.
type detail int

const (
	// detailLine is the one-line summary: method, url, status, timing.
	detailLine detail = iota
	// detailHeaders adds the headers of whichever half the row is.
	detailHeaders
	// detailBody adds the body too, pretty-printed.
	detailBody
)

func (d detail) next() detail {
	if d == detailBody {
		return detailLine
	}
	return d + 1
}

func (d detail) String() string {
	switch d {
	case detailHeaders:
		return "headers"
	case detailBody:
		return "headers+body"
	}
	return "one line"
}

// entryKind says which stream a timeline row came from.
type entryKind int

const (
	entryLog entryKind = iota
	entryExchange
)

// entry is one line of the merged view: a log line, or a whole exchange. An exchange sits at the
// time its request went out, which is where it belongs among the log lines around it, and the same
// line gains its status and timing once the response arrives.
type entry struct {
	at   time.Time
	kind entryKind
	// seq breaks ties so two things in the same millisecond keep the order they arrived in.
	seq int
	// text is the log line, for entryLog.
	text string
	// flow is the exchange, for entryRequest and entryResponse.
	flow proxy.Flow
}

// timeline holds both streams in one time-ordered list. Log lines arrive with the device's own
// timestamp; a line sims cannot read a time from is stamped on arrival, which keeps it in the right
// place relative to everything around it.
type timeline struct {
	mu      sync.Mutex
	entries []entry
	max     int
	seq     int
	// seen keeps a flow from being added twice: the store reports a flow while it is still running
	// and again when it finishes.
	seen map[int64]int
}

func newTimeline(max int) *timeline {
	if max <= 0 {
		max = logBuffer
	}
	return &timeline{max: max, seen: map[int64]int{}}
}

func (t *timeline) addLog(line string, fallback time.Time) {
	at, ok := parseLogTime(line, fallback)
	if !ok {
		at = fallback
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.push(entry{at: at, kind: entryLog, text: line})
}

// setFlows replaces what the timeline knows about the captured exchanges. A flow shows up as a
// request when it starts and gains a response line once it finishes.
func (t *timeline) setFlows(flows []proxy.Flow) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, f := range flows {
		state := 1
		if f.Done {
			state = 2
		}
		if t.seen[f.ID] >= state {
			continue
		}
		if t.seen[f.ID] == 0 {
			t.push(entry{at: f.Start, kind: entryExchange, flow: f})
		} else {
			// the line is already on the timeline at its request time; finishing only fills it in
			t.update(f)
		}
		t.seen[f.ID] = state
	}
}

// update replaces a flow already on the timeline, leaving it where its request put it.
func (t *timeline) update(f proxy.Flow) {
	for i := range t.entries {
		if t.entries[i].kind == entryExchange && t.entries[i].flow.ID == f.ID {
			t.entries[i].flow = f
			return
		}
	}
}

// push keeps the list ordered by time. Both streams arrive roughly in order, so the insert walks
// back from the end rather than sorting the whole list.
func (t *timeline) push(e entry) {
	t.seq++
	e.seq = t.seq
	i := len(t.entries)
	for i > 0 && t.entries[i-1].at.After(e.at) {
		i--
	}
	t.entries = append(t.entries, entry{})
	copy(t.entries[i+1:], t.entries[i:])
	t.entries[i] = e
	if len(t.entries) > t.max {
		drop := t.entries[0]
		t.entries = t.entries[1:]
		if drop.kind != entryLog {
			delete(t.seen, drop.flow.ID)
		}
	}
}

func (t *timeline) all() []entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]entry(nil), t.entries...)
}

func (t *timeline) clear() {
	t.mu.Lock()
	t.entries, t.seen = nil, map[int64]int{}
	t.mu.Unlock()
}

// logTimeLayouts are what the two platforms put at the head of a log line: iOS writes a full date,
// Android's `-v time` leaves the year out.
var logTimeLayouts = []string{"2006-01-02 15:04:05.000", "01-02 15:04:05.000"}

// parseLogTime reads the timestamp a log line starts with. The year is missing on Android, so it is
// taken from the stream's own clock; near new year that is the only way to get it right.
func parseLogTime(line string, ref time.Time) (time.Time, bool) {
	for _, layout := range logTimeLayouts {
		if len(line) < len(layout) {
			continue
		}
		at, err := time.ParseInLocation(layout, line[:len(layout)], time.Local)
		if err != nil {
			continue
		}
		if at.Year() == 0 {
			at = at.AddDate(ref.Year(), 0, 0)
			// a line from late December read in early January belongs to the year before
			if at.Sub(ref) > 24*time.Hour {
				at = at.AddDate(-1, 0, 0)
			}
		}
		return at, true
	}
	return ref, false
}

// stripLogTime removes the timestamp the row already shows in its own column.
func stripLogTime(line string) string {
	for _, layout := range logTimeLayouts {
		if len(line) < len(layout) {
			continue
		}
		if _, err := time.ParseInLocation(layout, line[:len(layout)], time.Local); err == nil {
			return strings.TrimSpace(line[len(layout):])
		}
	}
	return line
}

// render is what this entry contributes to the stream: one line, or that line followed by the
// headers and body when the reader has opened it.
func (e entry) render(filter string, d detail) string {
	stamp := fmt.Sprintf("[gray]%s[-]", e.at.Format("15:04:05.000"))
	if e.kind == entryLog {
		return fmt.Sprintf("%s  %s", stamp, highlight(stripLogTime(e.text), filter))
	}
	// size and timing come before the url: a long url wraps, and anything after it wraps with it
	head := fmt.Sprintf("%s  %s %s %s %s%s",
		stamp, outcomeCell(e.flow), sizeAndTiming(e.flow), methodCell(e.flow.Method, filter),
		highlight(requestLabel(e.flow), filter), trailingNote(e.flow))
	if d == detailLine {
		return head
	}
	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "\n%s[aqua]REQUEST[-]", detailIndent)
	b.WriteString(detailBlock(d, e.flow.ReqHeader, e.flow.ReqBody, e.flow.ReqSize, e.flow.ReqTruncated))
	if e.flow.Kind == proxy.KindTunnel {
		return b.String()
	}
	fmt.Fprintf(&b, "\n%s[aqua]RESPONSE[-]", detailIndent)
	b.WriteString(detailBlock(d, e.flow.RespHeader, e.flow.RespBody, e.flow.RespSize, e.flow.RespTruncated))
	return b.String()
}

// outcomeCell is the status once there is one, and a marker while the call is still out.
func outcomeCell(f proxy.Flow) string {
	if !f.Done {
		return "[gray]...[-]"
	}
	if f.Error != "" {
		return "[red]err[-]"
	}
	// a client that stopped reading still got the status it asked for, so the status is what shows
	return statusCell(f)
}

// sizeAndTiming is what the response added to the line. It is padded to a fixed width so the urls
// under each other start in the same column instead of stepping in and out with each size.
func sizeAndTiming(f proxy.Flow) string {
	if !f.Done || f.Error != "" {
		// nothing measured yet, or nothing to measure: the column still has to hold its place
		return fmt.Sprintf("[gray]%*s[-]", sizeWidth+timingWidth+1, "")
	}
	if f.Abandoned {
		// the size is what arrived before the client stopped reading, not the whole response
		return fmt.Sprintf("[gray]%*s %*s[-]",
			sizeWidth, proxy.SizeString(f.RespSize)+"+",
			timingWidth, fmt.Sprintf("%dms", f.Duration.Milliseconds()))
	}
	return fmt.Sprintf("[gray]%*s %*s[-]",
		sizeWidth, proxy.SizeString(f.RespSize),
		timingWidth, fmt.Sprintf("%dms", f.Duration.Milliseconds()))
}

// methodCell pads the method so every url starts in the same column. A method wider than the pad
// pushes its own url along rather than being cut short.
func methodCell(method, filter string) string {
	pad := methodWidth - len([]rune(method))
	if pad < 0 {
		pad = 0
	}
	return highlight(method, filter) + strings.Repeat(" ", pad)
}

// methodWidth fits the verbs in ordinary use; CONNECT and OPTIONS are the longest.
const methodWidth = 7

// sizeWidth and timingWidth hold the two columns still. "999.9 kB" and a five digit millisecond
// count are the widest ordinary values; anything longer pushes the url along rather than being cut.
const (
	sizeWidth   = 8
	timingWidth = 7
)

// trailingNote is what goes after the url, where a long one may wrap it out of sight: the sender,
// and the reason a failed exchange has no size or timing to show.
func trailingNote(f proxy.Flow) string {
	note := senderNote(f)
	if f.Abandoned {
		note = "  [yellow]client closed early[-]" + note
	}
	if f.Error != "" {
		note = "  [red]" + tview.Escape(f.Error) + "[-]" + note
	}
	return note
}

// detailIndent lines the opened text up under the summary rather than against the left edge, so the
// stream still reads as a stream.
const detailIndent = "              "

// detailBlock is the headers, and then the body, that an opened exchange shows under its summary.
func detailBlock(d detail, header http.Header, body []byte, size int64, truncated bool) string {
	if d == detailLine {
		return ""
	}
	var b strings.Builder
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, val := range header[k] {
			fmt.Fprintf(&b, "\n%s[aqua]%s:[-] %s", detailIndent, tview.Escape(k), tview.Escape(val))
		}
	}
	if len(keys) == 0 {
		fmt.Fprintf(&b, "\n%s[gray]no headers[-]", detailIndent)
	}
	if d != detailBody {
		if size > 0 {
			fmt.Fprintf(&b, "\n%s[gray]body %s, press o again to read it[-]", detailIndent, proxy.SizeString(size))
		}
		return b.String()
	}
	if size == 0 {
		fmt.Fprintf(&b, "\n%s[gray]no body[-]", detailIndent)
		return b.String()
	}
	fmt.Fprintf(&b, "\n%s[gray]body %s[-]", detailIndent, proxy.SizeString(size))
	for _, line := range strings.Split(proxy.Pretty(header, body), "\n") {
		fmt.Fprintf(&b, "\n%s%s", detailIndent, tview.Escape(line))
	}
	if truncated {
		fmt.Fprintf(&b, "\n%s[gray]...the rest was not kept[-]", detailIndent)
	}
	return b.String()
}

// senderNote names the process behind a request when the platform lets sims see one. On a phone it
// cannot, and the absence is the honest answer: the row is the device's traffic, not provably the
// app whose log is on the same screen.
func senderNote(f proxy.Flow) string {
	if f.Process == "" {
		return ""
	}
	return fmt.Sprintf("  [gray]%s[-]", tview.Escape(f.Process))
}

func requestLabel(f proxy.Flow) string {
	if f.Kind == proxy.KindTunnel {
		return f.Host
	}
	return f.URL
}

func responseArrow(f proxy.Flow) string {
	if f.Error != "" {
		return "[red]←[-]"
	}
	return "[green]←[-]"
}

func responseNote(f proxy.Flow) string {
	if f.Error != "" {
		return "[red]" + tview.Escape(f.Error) + "[-]"
	}
	return fmt.Sprintf("[gray]%dms[-]", f.Duration.Milliseconds())
}

// matches decides whether the filter keeps this row. A filter that hides a response but keeps its
// request would be worse than useless, so an exchange matches on either half.
func (e entry) matches(filter string) bool {
	if filter == "" {
		return true
	}
	needle := strings.ToLower(filter)
	if e.kind == entryLog {
		return strings.Contains(strings.ToLower(e.text), needle)
	}
	hay := e.flow.Method + " " + e.flow.URL + " " + e.flow.Host + " " + e.flow.Process
	return strings.Contains(strings.ToLower(hay), needle)
}

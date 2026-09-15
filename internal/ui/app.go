package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
)

type view interface {
	Name() string
	Hints() []hint
	Primitive() tview.Primitive
	Refresh()
}

type App struct {
	tv        *tview.Application
	root      *tview.Flex
	header    *header
	status    *tview.TextView
	cmd       *tview.InputField
	body      *tview.Pages
	providers map[device.Platform]device.Provider
	missing   map[device.Platform]error
	stack     []view
	ctx       context.Context
	cancel    context.CancelFunc
	version   string
}

func New(version string, providers ...device.Provider) *App {
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		tv:        tview.NewApplication(),
		providers: map[device.Platform]device.Provider{},
		missing:   map[device.Platform]error{},
		ctx:       ctx,
		cancel:    cancel,
		version:   version,
	}
	for _, p := range providers {
		if err := p.Available(); err != nil {
			a.missing[p.Platform()] = err
			continue
		}
		a.providers[p.Platform()] = p
	}
	a.build()
	return a
}

// Backgrounds stay ColorDefault so the terminal's own theme shows through, as k9s does.
func applyTheme() {
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.ContrastBackgroundColor = tcell.ColorDefault
	tview.Styles.MoreContrastBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	tview.Styles.BorderColor = tcell.ColorGray
	tview.Styles.TitleColor = tcell.ColorDefault
	tview.Styles.GraphicsColor = tcell.ColorGray
}

func (a *App) build() {
	applyTheme()
	a.header = newHeader(a.version)
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.body = tview.NewPages()
	a.cmd = newInput(":")
	a.cmd.SetDoneFunc(a.onCommand)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header.flex, headerHeight, 0, false).
		AddItem(a.body, 0, 1, true).
		AddItem(a.status, 1, 0, false)
	a.cmd.SetBorder(true).SetBorderColor(tcell.ColorAqua)

	a.tv.SetRoot(a.root, true).EnableMouse(false)
	a.tv.SetInputCapture(a.onKey)
	a.push(newDevicesView(a))
	a.header.loadFacts(a.ctx, a.providers, a.missing, func() { a.tv.QueueUpdateDraw(a.drawHeader) })
	go pollUsage(a.ctx, 3*time.Second, func(u usage) {
		a.tv.QueueUpdateDraw(func() { a.header.usage = u; a.drawHeader() })
	})
}

func (a *App) Run() error {
	defer a.cancel()
	return a.tv.Run()
}

func (a *App) push(v view) {
	a.stack = append(a.stack, v)
	a.body.AddAndSwitchToPage(v.Name(), v.Primitive(), true)
	a.tv.SetFocus(v.Primitive())
	a.drawHeader()
	v.Refresh()
}

func (a *App) pop() {
	if len(a.stack) <= 1 {
		return
	}
	top := a.stack[len(a.stack)-1]
	a.stack = a.stack[:len(a.stack)-1]
	a.body.RemovePage(top.Name())
	cur := a.top()
	a.body.SwitchToPage(cur.Name())
	a.tv.SetFocus(cur.Primitive())
	a.drawHeader()
	cur.Refresh()
}

func (a *App) top() view { return a.stack[len(a.stack)-1] }

func (a *App) replaceTop(v view) {
	if len(a.stack) > 1 {
		old := a.stack[len(a.stack)-1]
		a.stack = a.stack[:len(a.stack)-1]
		a.body.RemovePage(old.Name())
	}
	a.push(v)
}

func (a *App) drawHeader() {
	a.header.draw(a.top().Hints())
}

func (a *App) crumbs() string {
	names := make([]string, 0, len(a.stack))
	for _, v := range a.stack {
		names = append(names, v.Name())
	}
	return strings.Join(names, " > ")
}

func (a *App) onKey(ev *tcell.EventKey) *tcell.EventKey {
	if a.tv.GetFocus() == a.cmd {
		if ev.Key() == tcell.KeyEscape {
			a.closeCommand()
			return nil
		}
		return ev
	}
	if _, editing := a.tv.GetFocus().(*tview.InputField); editing {
		return ev
	}
	switch {
	case ev.Rune() == ':':
		a.openCommand()
		return nil
	case ev.Rune() == '?':
		a.push(newHelpView(a))
		return nil
	case ev.Key() == tcell.KeyEscape:
		a.pop()
		return nil
	case ev.Key() == tcell.KeyCtrlC:
		a.tv.Stop()
		return nil
	case ev.Rune() == 'r', ev.Key() == tcell.KeyF5:
		a.top().Refresh()
		return nil
	}
	return ev
}

// The command bar drops in under the header, where k9s puts it, instead of replacing the status line.
func (a *App) openCommand() {
	a.cmd.SetText("")
	a.root.RemoveItem(a.cmd)
	a.root.Clear()
	a.root.AddItem(a.header.flex, headerHeight, 0, false).
		AddItem(a.cmd, 3, 0, true).
		AddItem(a.body, 0, 1, false).
		AddItem(a.status, 1, 0, false)
	a.tv.SetFocus(a.cmd)
}

func (a *App) closeCommand() {
	a.root.Clear()
	a.root.AddItem(a.header.flex, headerHeight, 0, false).
		AddItem(a.body, 0, 1, true).
		AddItem(a.status, 1, 0, false)
	a.tv.SetFocus(a.top().Primitive())
}

func (a *App) onCommand(key tcell.Key) {
	if key != tcell.KeyEnter {
		return
	}
	text := strings.TrimSpace(a.cmd.GetText())
	a.closeCommand()
	switch text {
	case "":
	case "dev", "devices", "d":
		a.stack = a.stack[:1]
		for _, name := range a.body.GetPageNames(false) {
			if name != a.stack[0].Name() {
				a.body.RemovePage(name)
			}
		}
		a.body.SwitchToPage(a.stack[0].Name())
		a.tv.SetFocus(a.stack[0].Primitive())
		a.drawHeader()
		a.stack[0].Refresh()
	case "img", "images", "i":
		a.replaceTop(newImagesView(a))
	case "apps", "a", "logs", "l":
		d, ok := a.selectedDevice()
		if !ok {
			a.flash("select a device first")
			return
		}
		if text == "apps" || text == "a" {
			a.replaceTop(newAppsView(a, d))
		} else {
			a.replaceTop(newLogsView(a, d))
		}
	case "help", "h", "?":
		a.push(newHelpView(a))
	default:
		if fields := strings.Fields(text); len(fields) >= 2 && (fields[0] == "connect" || fields[0] == "pair") {
			a.wirelessCommand(fields)
			return
		}
		a.flash("unknown command: " + text)
	}
}

// adb connect blocks for over a minute on an unreachable host, so the command gets its own deadline.
func (a *App) wirelessCommand(fields []string) {
	w, ok := a.providers[device.PlatformAndroid].(device.Wireless)
	if !ok {
		a.flash("android provider is not available")
		return
	}
	verb, addr := fields[0], fields[1]
	code := ""
	if len(fields) > 2 {
		code = fields[2]
	}
	a.status.SetText(fmt.Sprintf(" %s %s...", verb, addr))
	a.async(func() error {
		ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
		defer cancel()
		if verb == "pair" {
			return w.Pair(ctx, addr, code)
		}
		return w.Connect(ctx, addr)
	}, func() {
		a.flash(fmt.Sprintf("%s %s: ok", verb, addr))
		a.stack[0].Refresh()
	})
}

func (a *App) selectedDevice() (device.Device, bool) {
	for i := len(a.stack) - 1; i >= 0; i-- {
		if dv, ok := a.stack[i].(*devicesView); ok {
			return dv.selected()
		}
	}
	return device.Device{}, false
}

func (a *App) flash(msg string) {
	a.status.SetText(" " + tview.Escape(msg))
	go func() {
		time.Sleep(4 * time.Second)
		a.tv.QueueUpdateDraw(func() {
			if strings.TrimSpace(a.status.GetText(true)) == msg {
				a.status.SetText("")
			}
		})
	}()
}

func (a *App) flashErr(err error) {
	a.status.SetText(" [red]" + tview.Escape(err.Error()) + "[-]")
}

// work must not touch tview; only then runs on the UI goroutine.
func (a *App) async(work func() error, then func()) {
	go func() {
		err := work()
		a.tv.QueueUpdateDraw(func() {
			if err != nil {
				a.flashErr(err)
				return
			}
			if then != nil {
				then()
			}
		})
	}()
}

func (a *App) confirm(question string, onYes func()) {
	modal := tview.NewModal().SetText(question).AddButtons([]string{"Yes", "No"})
	modal.SetBackgroundColor(tcell.ColorDefault)
	modal.SetButtonStyle(buttonStyle).SetButtonActivatedStyle(focusStyle)
	modal.SetDoneFunc(func(_ int, label string) {
		a.body.RemovePage("confirm")
		a.tv.SetFocus(a.top().Primitive())
		if label == "Yes" {
			onYes()
		}
	})
	a.body.AddPage("confirm", modal, true, true)
	a.tv.SetFocus(modal)
}

func (a *App) prompt(label, initial string, onDone func(string)) {
	in := newInput(label + " ").SetText(initial).SetFieldWidth(0)
	in.SetDoneFunc(func(key tcell.Key) {
		text := in.GetText()
		a.root.RemoveItem(in)
		a.root.AddItem(a.status, 1, 0, false)
		a.tv.SetFocus(a.top().Primitive())
		if key == tcell.KeyEnter && text != "" {
			onDone(text)
		}
	})
	a.root.RemoveItem(a.status)
	a.root.AddItem(in, 1, 0, true)
	a.tv.SetFocus(in)
}

func (a *App) promptPath(label, initial string, onDone func(string)) {
	in := newInput(label + " ").SetText(initial).SetFieldWidth(0)
	in.SetAutocompleteFunc(func(current string) []string {
		if len(current) == 0 {
			return nil
		}
		return completePath(current)
	})
	in.SetAutocompleteStyles(tcell.ColorDefault, fieldStyle, focusStyle)
	in.SetDoneFunc(func(key tcell.Key) {
		// tab with no completion list open would otherwise close the prompt
		if key == tcell.KeyTab || key == tcell.KeyBacktab {
			return
		}
		text := in.GetText()
		a.root.RemoveItem(in)
		a.root.AddItem(a.status, 1, 0, false)
		a.tv.SetFocus(a.top().Primitive())
		if key == tcell.KeyEnter && text != "" {
			onDone(text)
		}
	})
	a.root.RemoveItem(a.status)
	a.root.AddItem(in, 1, 0, true)
	a.tv.SetFocus(in)
}

func (a *App) sendKey(d device.Device, key device.Key) {
	p, err := a.providerFor(d)
	if err != nil {
		a.flashErr(err)
		return
	}
	ks, ok := p.(device.KeySender)
	if !ok {
		a.flashErr(fmt.Errorf("%s cannot send %s from here; use the simulator window", d.Platform, key))
		return
	}
	a.async(func() error { return ks.SendKey(a.ctx, d, key) }, func() { a.flash(string(key) + " sent to " + d.Name) })
}

func (a *App) providerFor(d device.Device) (device.Provider, error) {
	p, ok := a.providers[d.Platform]
	if !ok {
		return nil, fmt.Errorf("no provider for %s", d.Platform)
	}
	return p, nil
}

// Form fields get a visible box and the focused element inverts, so the cursor's owner is never ambiguous
// on a transparent background.
var (
	fieldStyle  = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
	focusStyle  = tcell.StyleDefault.Background(tcell.ColorDarkCyan).Foreground(tcell.ColorBlack)
	buttonStyle = tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite)
)

func newTable() *tview.Table {
	t := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	t.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	t.SetTitleColor(titleColor())
	t.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkCyan).Foreground(tcell.ColorBlack))
	return t
}

func newInput(label string) *tview.InputField {
	in := tview.NewInputField().SetLabel(label)
	in.SetFieldBackgroundColor(tcell.ColorDefault).SetFieldTextColor(tcell.ColorDefault).SetLabelColor(tcell.ColorYellow)
	return in
}

func setHeader(t *tview.Table, cols ...string) {
	for i, c := range cols {
		t.SetCell(0, i, tview.NewTableCell("[::b]"+c).SetSelectable(false).SetExpansion(1).SetTextColor(tcell.ColorAqua))
	}
}

func stateColor(s device.State) string {
	switch s {
	case device.StateBooted:
		return "[green]"
	case device.StateBooting:
		return "[yellow]"
	case device.StateShutdown:
		return "[gray]"
	}
	return "[red]"
}

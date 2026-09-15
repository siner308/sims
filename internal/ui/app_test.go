package ui

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
)

type fakeProvider struct {
	platform device.Platform
	devices  []device.Device
	delay    time.Duration
}

func (f *fakeProvider) Platform() device.Platform { return f.platform }
func (f *fakeProvider) Available() error          { return nil }
func (f *fakeProvider) List(ctx context.Context) ([]device.Device, error) {
	time.Sleep(f.delay)
	return f.devices, nil
}
func (f *fakeProvider) Boot(context.Context, device.Device) error     { return nil }
func (f *fakeProvider) Shutdown(context.Context, device.Device) error { return nil }
func (f *fakeProvider) Erase(context.Context, device.Device) error    { return nil }
func (f *fakeProvider) Delete(context.Context, device.Device) error   { return nil }
func (f *fakeProvider) Apps(context.Context, device.Device) ([]device.App, error) {
	return []device.App{
		{BundleID: "com.android.settings", Name: "Settings", System: true, Source: "preinstalled"},
		{BundleID: "com.android.phone", Name: "Phone", System: true, Source: "preinstalled"},
	}, nil
}
func (f *fakeProvider) InstallApp(context.Context, device.Device, string) error   { return nil }
func (f *fakeProvider) UninstallApp(context.Context, device.Device, string) error { return nil }
func (f *fakeProvider) LaunchApp(context.Context, device.Device, string) error    { return nil }
func (f *fakeProvider) LogCmd(context.Context, device.Device, *device.App) (*exec.Cmd, error) {
	return nil, nil
}
func (f *fakeProvider) Images(context.Context) ([]device.Image, error)   { return nil, nil }
func (f *fakeProvider) InstallImage(context.Context, device.Image) error { return nil }
func (f *fakeProvider) Create(context.Context, string, device.Image, string, *device.Hardware) error {
	return nil
}
func (f *fakeProvider) DeviceTypes(context.Context) ([]device.DeviceType, error) { return nil, nil }

func runHeadless(t *testing.T, a *App) (tcell.SimulationScreen, func()) {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(140, 40)
	a.tv.SetScreen(screen)
	done := make(chan error, 1)
	go func() { done <- a.tv.Run() }()
	return screen, func() {
		a.tv.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("app did not stop")
		}
	}
}

// onUI runs fn on the tview goroutine; widget reads from the test goroutine race with Draw.
func onUI(a *App, fn func()) { a.tv.QueueUpdate(fn) }

func waitFor(t *testing.T, a *App, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok := false
		a.tv.QueueUpdate(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var status string
	a.tv.QueueUpdate(func() { status = a.status.GetText(true) })
	t.Fatalf("condition not met within %s; status=%q", timeout, status)
}

func TestApp_DevicesRender(t *testing.T) {
	android := &fakeProvider{platform: device.PlatformAndroid, delay: 200 * time.Millisecond, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, Runtime: "API 35", State: device.StateBooted, Serial: "emulator-5554"},
		{ID: "R3C", Name: "SM S928N", Platform: device.PlatformAndroid, Kind: device.KindPhysical, Transport: device.TransportUSB, State: device.StateConnected, Serial: "R3C"},
	}}
	ios := &fakeProvider{platform: device.PlatformIOS, devices: []device.Device{
		{ID: "udid1", Name: "iPhone 17", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, Runtime: "iOS 26.4", State: device.StateShutdown, LastActiveAt: time.Now().Add(-time.Hour)},
	}}
	a := New("test", android, ios)
	_, stop := runHeadless(t, a)
	defer stop()

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 4 })

	var got string
	onUI(a, func() { got = dv.table.GetCell(3, 2).Text })
	if got != "iPhone 17" {
		t.Errorf("shutdown simulator should sort last, got %q", got)
	}
}

func TestApp_AppsSystemToggle(t *testing.T) {
	android := &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, State: device.StateBooted, Serial: "emulator-5554"},
	}}
	a := New("test", android)
	_, stop := runHeadless(t, a)
	defer stop()

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	var av *appsView
	a.tv.QueueUpdate(func() {
		dv.openApps()
		var ok bool
		av, ok = a.top().(*appsView)
		if !ok {
			t.Errorf("top view = %T (selected=%v)", a.top(), func() bool { _, ok := dv.selected(); return ok }())
		}
	})
	waitFor(t, a, 5*time.Second, func() bool { return av != nil && len(av.apps) == 2 })

	var hint, source string
	onUI(a, func() { hint = av.table.GetCell(1, 0).Text })
	if !strings.Contains(hint, "2 preinstalled apps hidden") {
		t.Fatalf("hidden hint missing, got %q", hint)
	}
	onUI(a, func() { av.showSystem = true; av.render() })
	waitFor(t, a, time.Second, func() bool { return av.table.GetRowCount() == 3 })
	onUI(a, func() { source = av.table.GetCell(1, 3).Text })
	if !strings.Contains(source, "preinstalled") {
		t.Errorf("SOURCE column = %q", source)
	}
}

func TestApp_PlainKeysAreNotDestructive(t *testing.T) {
	var shutdowns atomic.Int32
	android := &countingProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, State: device.StateBooted, Serial: "emulator-5554"},
	}}, onShutdown: func() { shutdowns.Add(1) }}
	a := New("test", android)
	_, stop := runHeadless(t, a)
	defer stop()

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	press := func(ev *tcell.EventKey) {
		a.tv.QueueUpdate(func() { dv.onKey(ev) })
	}
	press(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
	press(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))
	press(tcell.NewEventKey(tcell.KeyRune, 'e', tcell.ModNone))
	time.Sleep(200 * time.Millisecond)
	if n := shutdowns.Load(); n != 0 {
		t.Fatalf("plain s triggered shutdown %d time(s)", n)
	}

	press(tcell.NewEventKey(tcell.KeyCtrlK, 0, tcell.ModCtrl))
	waitFor(t, a, 2*time.Second, func() bool { return shutdowns.Load() == 1 })
}

type countingProvider struct {
	fakeProvider
	onShutdown func()
}

func (c *countingProvider) Shutdown(context.Context, device.Device) error {
	c.onShutdown()
	return nil
}

func TestApp_TransparentBackground(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, State: device.StateShutdown},
	}})
	screen, stop := runHeadless(t, a)
	defer stop()

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })
	a.tv.QueueUpdateDraw(func() {})

	var w, h int
	var cells []tcell.SimCell
	onUI(a, func() {
		w, h = screen.Size()
		cells, _, _ = screen.GetContents()
	})
	nonDefault := map[tcell.Color]int{}
	for i := 0; i < w*h && i < len(cells); i++ {
		_, bg, _ := cells[i].Style.Decompose()
		if bg != tcell.ColorDefault {
			nonDefault[bg]++
		}
	}
	if len(cells) == 0 {
		t.Fatal("screen has no cells; nothing was drawn")
	}
	if nonDefault[tcell.ColorDarkCyan] == 0 {
		t.Fatal("selection bar not painted; the check would be vacuous")
	}
	// the only painted background allowed is the selection bar
	for bg, n := range nonDefault {
		if bg != tcell.ColorDarkCyan {
			t.Errorf("background %v painted on %d cells; expected terminal default", bg, n)
		}
	}
}

func TestDevicesView_Sort(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	devs := []device.Device{
		{Name: "shutdown-recent", Runtime: "API 35", State: device.StateShutdown, LastActiveAt: now.Add(-time.Hour)},
		{Name: "offline", Runtime: "iOS 27.0", State: device.StateOffline},
		{Name: "booted-old", Runtime: "iOS 18.5", State: device.StateBooted, LastActiveAt: now.Add(-48 * time.Hour)},
		{Name: "booted-new", Runtime: "API 36", State: device.StateConnected, LastActiveAt: now.Add(-time.Minute)},
		{Name: "shutdown-never", Runtime: "iOS 26.4", State: device.StateShutdown},
	}
	names := func(v *devicesView) []string {
		out := make([]string, 0, len(v.devices))
		for _, d := range v.devices {
			out = append(out, d.Name)
		}
		return out
	}
	v := &devicesView{table: newTable(), now: func() time.Time { return now }, sort: defaultSort}
	v.devices = append([]device.Device(nil), devs...)

	v.render()
	want := []string{"booted-new", "booted-old", "offline", "shutdown-recent", "shutdown-never"}
	if got := names(v); !slices.Equal(got, want) {
		t.Errorf("default sort = %v, want %v", got, want)
	}
	if got := v.table.GetCell(0, int(colState)).Text; got != "[::b]STATE^" {
		t.Errorf("state header = %q", got)
	}
	if got := v.table.GetCell(1, 6).Text; got != "1m ago" {
		t.Errorf("LAST for booted-new = %q", got)
	}
	if got := v.table.GetCell(5, 6).Text; got != "-" {
		t.Errorf("LAST for never-booted = %q", got)
	}

	// same state and same LAST: name desc, then runtime desc
	v.devices = []device.Device{
		{Name: "iPhone 16", Runtime: "iOS 18.5", State: device.StateShutdown},
		{Name: "iPhone 17", Runtime: "iOS 26.4", State: device.StateShutdown},
		{Name: "iPhone 17", Runtime: "iOS 26.5", State: device.StateShutdown},
	}
	v.render()
	if v.devices[0].Runtime != "iOS 26.5" || v.devices[2].Name != "iPhone 16" {
		t.Errorf("tie order = %v", names(v))
	}
	v.devices = append([]device.Device(nil), devs...)

	v.setSort(colName)
	if got := names(v)[0]; got != "booted-new" || v.sort.desc {
		t.Errorf("shift+N asc first = %q desc=%v", got, v.sort.desc)
	}
	v.setSort(colName)
	if got := names(v)[0]; got != "shutdown-recent" || !v.sort.desc {
		t.Errorf("shift+N again should flip to desc, first = %q", got)
	}

	v.setSort(colRuntime)
	if got := v.devices[0].Runtime; got != "API 35" {
		t.Errorf("shift+R asc first runtime = %q", got)
	}

	v.setSort(colLast)
	if !v.sort.desc || names(v)[0] != "booted-new" {
		t.Errorf("shift+L should start desc (most recent first), got %v desc=%v", names(v), v.sort.desc)
	}
	if got := v.table.GetCell(0, int(colLast)).Text; got != "[::b]LASTv" {
		t.Errorf("last header = %q", got)
	}
}

func TestApp_CommandBarUnderHeader(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid})
	screen, stop := runHeadless(t, a)
	defer stop()

	a.tv.QueueUpdateDraw(func() { a.openCommand() })
	var h, y int
	var focused tview.Primitive
	onUI(a, func() {
		_, h = screen.Size()
		_, y, _, _ = a.cmd.GetRect()
		focused = a.tv.GetFocus()
	})
	if y != a.header.height {
		t.Errorf("command bar y = %d, want %d (directly under the header)", y, a.header.height)
	}
	if y > h/2 {
		t.Errorf("command bar drawn in the bottom half (y=%d of %d)", y, h)
	}
	if focused != a.cmd {
		t.Error("command bar did not take focus")
	}

	a.tv.QueueUpdateDraw(func() { a.closeCommand() })
	onUI(a, func() { focused = a.tv.GetFocus() })
	if focused != a.top().Primitive() {
		t.Error("focus did not return to the view")
	}
}

func TestApp_PlainQDoesNotQuit(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid})
	_, stop := runHeadless(t, a)
	defer stop()

	stopped := false
	a.tv.QueueUpdate(func() {
		if ev := a.onKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)); ev == nil {
			stopped = true
		}
	})
	if stopped {
		t.Fatal("plain q was consumed as quit")
	}
}

func TestCreateView_ArrowNavigation(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid})
	_, stop := runHeadless(t, a)
	defer stop()

	var cv *createView
	a.tv.QueueUpdate(func() {
		cv = newCreateView(a, a.providers[device.PlatformAndroid], device.Image{ID: "img", Name: "img", Version: "1"}, []device.DeviceType{{ID: "pixel_7", Name: "pixel_7", Screen: "1080x2400"}, {ID: "pixel_8", Name: "pixel_8"}})
		a.push(cv)
	})
	press := func(k tcell.Key) {
		a.tv.QueueUpdate(func() { cv.onKey(tcell.NewEventKey(k, 0, tcell.ModNone)) })
	}
	focused := func() (int, int) {
		var item, button int
		a.tv.QueueUpdate(func() { item, button = cv.form.GetFocusedItemIndex() })
		return item, button
	}

	if item, _ := focused(); item != 0 {
		t.Fatalf("initial focus item = %d, want 0 (name)", item)
	}
	press(tcell.KeyDown)
	if item, _ := focused(); item != 1 {
		t.Errorf("down: item = %d, want 1 (device type)", item)
	}
	press(tcell.KeyDown)
	if _, button := focused(); button != 0 {
		t.Errorf("down past device type: button = %d, want 0 (create)", button)
	}
	press(tcell.KeyRight)
	if _, button := focused(); button != 1 {
		t.Errorf("right: button = %d, want 1 (cancel)", button)
	}
	press(tcell.KeyLeft)
	if _, button := focused(); button != 0 {
		t.Errorf("left: button = %d, want 0 (create)", button)
	}
	press(tcell.KeyUp)
	if item, _ := focused(); item != 1 {
		t.Errorf("up from button: item = %d, want 1", item)
	}
	if fg, bg, _ := fieldStyle.Decompose(); fg == tcell.ColorDefault && bg == tcell.ColorDefault {
		t.Error("form field style is invisible on a transparent background")
	}
	if fg, bg, _ := focusStyle.Decompose(); fg == tcell.ColorDefault && bg == tcell.ColorDefault {
		t.Error("focus style is invisible on a transparent background")
	}
}

func TestHeader_Render(t *testing.T) {
	h := newHeader("v0.1")
	h.facts = [][2]string{{"Platforms", "android, ios"}, {"Android SDK", "/sdk"}, {"Tools", "adb 36.0.0, emulator 36.1.9.0, Xcode 26.6"}}
	h.usage = usage{cpu: 12, mem: 75, disk: 91, free: 3 << 30, ok: true}
	h.draw([]hint{{"b", "boot"}, {"ctrl+k", "shutdown"}, {"enter", "apps"}})

	info := h.info.GetText(true)
	for _, want := range []string{"Platforms:", "android, ios", "Android SDK:", "Tools:", "Xcode 26.6", "CPU:", "12%", "MEM:", "75%", "DISK:", "91%", "3.0 GiB free"} {
		if !strings.Contains(info, want) {
			t.Errorf("info panel missing %q in %q", want, info)
		}
	}
	if lineCount(info) != 6 {
		t.Errorf("info panel lines = %d, want 6", lineCount(info))
	}
	if !strings.Contains(h.logo.GetText(true), "v0.1") {
		t.Error("version should be shown under the logo")
	}
	keys := h.keys.GetText(true)
	if !strings.HasPrefix(strings.TrimLeft(keys, " "), "<:>") {
		t.Errorf("global keys should lead the hotkey block: %q", strings.SplitN(keys, "\n", 2)[0])
	}
	for _, want := range []string{"<b>", "boot", "<ctrl+k>", "shutdown", "<:>", "command", "<ctrl+c>", "quit"} {
		if !strings.Contains(keys, want) {
			t.Errorf("hotkeys missing %q in %q", want, keys)
		}
	}
	if h.height < lineCount(keys) || h.height < lineCount(info) {
		t.Errorf("header height %d smaller than its content (%d key lines, %d info lines)", h.height, lineCount(keys), lineCount(info))
	}
	raw := h.info.GetText(false)
	if !strings.Contains(raw, "[red]91%") || !strings.Contains(raw, "[yellow]75%") {
		t.Errorf("usage colors not applied: %q", raw)
	}
	if !strings.Contains(h.logo.GetText(true), "|___/") {
		t.Error("logo missing")
	}
}

func TestRenderHints_GroupsAreColumns(t *testing.T) {
	hints := []hint{{"a", "1"}, {"b", "2"}, groupBreak, {"c", "3"}, groupBreak, {"d", "4"}, {"e", "5"}, {"f", "6"}, {"g", "7"}}
	lines := strings.Split(strings.TrimRight(renderHints(hints, 0), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("rows = %d, want 4 (tallest group)", len(lines))
	}
	for _, k := range []string{"<a>", "<c>", "<d>"} {
		if !strings.Contains(lines[0], k) {
			t.Errorf("row 0 missing %s: %q", k, lines[0])
		}
	}
	if !strings.Contains(lines[3], "<g>") || strings.Contains(lines[1], "<c>") {
		t.Errorf("groups must stay in their own column: %q / %q", lines[1], lines[3])
	}
}

func TestHeader_GrowsWithHints(t *testing.T) {
	h := newHeader("v")
	many := make([]hint, 0, 9)
	for i := range 9 {
		many = append(many, hint{key: string(rune('a' + i)), label: "x"})
	}
	if got := h.draw(many); got != 9 {
		t.Errorf("height = %d, want 9 for a nine-key group", got)
	}
	if got := h.draw([]hint{{"a", "x"}}); got != minHeaderHeight {
		t.Errorf("height = %d, want the %d-line minimum", got, minHeaderHeight)
	}
}

func TestDevicesHints_NoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range newDevicesView(New("t")).Hints() {
		if h.isBreak() {
			continue
		}
		if seen[h.key] {
			t.Errorf("key %q listed twice", h.key)
		}
		seen[h.key] = true
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 3 << 30: "3.0 GiB", 1536 << 20: "1.5 GiB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

type keyProvider struct {
	fakeProvider
	mu   sync.Mutex
	sent []device.Key
}

func (k *keyProvider) SendKey(_ context.Context, _ device.Device, key device.Key) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sent = append(k.sent, key)
	return nil
}

func (k *keyProvider) sentKeys() []device.Key {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := append([]device.Key(nil), k.sent...)
	slices.Sort(out)
	return out
}

func TestDevicesView_SendsNavigationKeys(t *testing.T) {
	kp := &keyProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateBooted, Serial: "emulator-5554"},
	}}}
	a := New("test", kp)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone),
	} {
		a.tv.QueueUpdate(func() { dv.onKey(ev) })
	}
	// each key is sent from its own goroutine, so only the set is deterministic
	waitFor(t, a, 5*time.Second, func() bool { return len(kp.sentKeys()) == 3 })
	want := []device.Key{device.KeyHome, device.KeyBack, device.KeyOverview}
	slices.Sort(want)
	if got := kp.sentKeys(); !slices.Equal(got, want) {
		t.Errorf("sent = %v, want %v", got, want)
	}
}

func TestDevicesView_PairKeyIsWired(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "x", Name: "phone", Platform: device.PlatformAndroid, Kind: device.KindPhysical, State: device.StateConnected, Serial: "x"},
	}})
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })
	var status string
	onUI(a, func() {
		dv.onKey(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone))
		status = a.status.GetText(true)
	})
	// android has no Pairer, so a wired p key surfaces the ":pair" hint in the status line
	if !strings.Contains(status, ":pair") {
		t.Errorf("p did not reach pair(); status=%q", status)
	}
}

type bootProvider struct {
	fakeProvider
	bootCalls int
	mu        sync.Mutex
}

func (b *bootProvider) Boot(_ context.Context, d device.Device) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bootCalls++
	for i := range b.devices {
		if b.devices[i].ID == d.ID {
			b.devices[i].State = device.StateBooted
			b.devices[i].Serial = "emulator-5554"
		}
	}
	return nil
}

func (b *bootProvider) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bootCalls
}

func (b *bootProvider) List(ctx context.Context) ([]device.Device, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]device.Device(nil), b.devices...), nil
}

func TestDevicesView_EnterOnStoppedDeviceBootsThenOpensApps(t *testing.T) {
	bp := &bootProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, Transport: device.TransportAVD, State: device.StateShutdown},
	}}}
	a := New("test", bp)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	var isApps bool
	onUI(a, func() {
		dv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		_, isApps = a.top().(*appsView)
	})
	if bp.calls() != 0 {
		t.Fatal("boot ran before the user confirmed")
	}
	if isApps {
		t.Fatal("apps opened on a stopped device without booting")
	}
	// answer the confirm modal with Yes
	a.tv.QueueUpdate(func() {
		name, prim := a.body.GetFrontPage()
		if name != "confirm" {
			t.Errorf("front page = %q, want confirm modal", name)
			return
		}
		// No comes first so a stray enter is harmless; move to Yes before confirming
		handler := prim.(*tview.Modal).InputHandler()
		handler(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	})
	waitFor(t, a, 10*time.Second, func() bool { _, ok := a.top().(*appsView); return ok })
	if n := bp.calls(); n != 1 {
		t.Errorf("boot calls = %d, want 1", n)
	}
}

func TestDevicesView_EnterOnOfflinePhoneFlashesError(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "R3C", Name: "phone", Platform: device.PlatformAndroid, Kind: device.KindPhysical, Transport: device.TransportUSB, State: device.StateOffline},
	}})
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })
	var status string
	var isApps bool
	onUI(a, func() {
		dv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		status = a.status.GetText(true)
		_, isApps = a.top().(*appsView)
	})
	if !strings.Contains(status, "offline") || !strings.Contains(status, "plug it in") {
		t.Errorf("status = %q, want an offline hint for an android phone", status)
	}
	if isApps {
		t.Error("apps must not open for an offline phone")
	}
}

type connectProvider struct {
	fakeProvider
	connected atomic.Int32
}

func (c *connectProvider) Connect(context.Context, device.Device) error {
	c.connected.Add(1)
	return nil
}

func TestDevicesView_WOnIOSPhoneConnects(t *testing.T) {
	cp := &connectProvider{fakeProvider: fakeProvider{platform: device.PlatformIOS, devices: []device.Device{
		{ID: "BBBB", Name: "my iphone", Platform: device.PlatformIOS, Kind: device.KindPhysical, Transport: device.TransportWiFi, State: device.StateOffline},
	}}}
	a := New("test", cp)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	var status string
	onUI(a, func() {
		dv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		status = a.status.GetText(true)
	})
	if !strings.Contains(status, "press w") {
		t.Errorf("enter on an offline iphone should point at w, got %q", status)
	}
	onUI(a, func() { dv.onKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone)) })
	waitFor(t, a, 2*time.Second, func() bool { return cp.connected.Load() == 1 })
}

func TestRenderHints_DropsWholeColumnsThatDoNotFit(t *testing.T) {
	hints := []hint{{"a", "first"}, groupBreak, {"bb", "second"}, groupBreak, {"ccc", "third"}}
	full := renderHints(hints, 0)
	if !strings.Contains(full, "third") {
		t.Fatalf("unbounded render lost a column: %q", full)
	}
	// column widths: "<a> first" = 3+1+5+2 = 11, "<bb> second" = 4+1+6+2 = 13, "<ccc> third" = 5+1+5+2 = 13
	narrow := renderHints(hints, 30)
	if !strings.Contains(narrow, "second") || strings.Contains(narrow, "third") {
		t.Errorf("width 30 should keep two columns and drop the third whole: %q", narrow)
	}
	if strings.Contains(narrow, "thi") {
		t.Errorf("a column must never be cut mid-word: %q", narrow)
	}
}

func TestLogsView_WrapToggle(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid})
	_, stop := runHeadless(t, a)
	defer stop()
	var title string
	var nowrap bool
	onUI(a, func() {
		lv := newLogsView(a, device.Device{Name: "dev", State: device.StateBooted}, nil)
		lv.onKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
		title, nowrap = lv.text.GetTitle(), lv.nowrap
	})
	if !nowrap || !strings.Contains(title, "[nowrap]") {
		t.Errorf("w should turn wrapping off and mark the title, got nowrap=%v title=%q", nowrap, title)
	}
}

func TestDevicesView_RefreshesWhileShuttingDown(t *testing.T) {
	bp := &bootProvider{fakeProvider: fakeProvider{platform: device.PlatformIOS, devices: []device.Device{
		{ID: "u1", Name: "iPhone", Platform: device.PlatformIOS, Kind: device.KindVirtual, State: device.StateShuttingDown},
	}}}
	a := New("test", bp)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return len(dv.devices) == 1 && dv.devices[0].State == device.StateShuttingDown })
	bp.mu.Lock()
	bp.devices[0].State = device.StateShutdown
	bp.mu.Unlock()
	waitFor(t, a, 6*time.Second, func() bool { return dv.devices[0].State == device.StateShutdown })
}

func TestPrompt_EmptyEnterClearsFilter(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "a", Name: "alpha", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
		{ID: "b", Name: "beta", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
	}})
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 3 })

	var rows int
	onUI(a, func() { dv.filter = "alpha"; dv.render(); rows = dv.table.GetRowCount() })
	if rows != 2 {
		t.Fatalf("filtered rows = %d, want 2", rows)
	}
	var initial string
	onUI(a, func() {
		dv.onKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
		in := a.tv.GetFocus().(*tview.InputField)
		initial = in.GetText()
		in.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
		rows = dv.table.GetRowCount()
	})
	if initial != "" {
		t.Errorf("/ should open an empty prompt, not carry %q over", initial)
	}
	if rows != 3 || dv.filter != "" {
		t.Errorf("empty enter should clear the filter: rows=%d filter=%q", rows, dv.filter)
	}
}

func TestHighlight(t *testing.T) {
	if got := highlight("Pixel_7_API_35", "pixel"); got != "[black:yellow]Pixel[-:-]_7_API_35" {
		t.Errorf("highlight = %q", got)
	}
	if got := highlight("abcabc", "B"); got != "a[black:yellow]b[-:-]ca[black:yellow]b[-:-]c" {
		t.Errorf("highlight = %q", got)
	}
	// whatever the markup, the visible text must be the original, brackets included
	for _, c := range []struct{ text, needle string }{{"tag [x] here", "x"}, {"[red]literal[-]", "lit"}, {"nothing", "zzz"}, {"plain", ""}} {
		tv := tview.NewTextView().SetDynamicColors(true)
		tv.SetText(highlight(c.text, c.needle))
		if got := tv.GetText(true); got != c.text {
			t.Errorf("visible text for (%q, %q) = %q", c.text, c.needle, got)
		}
	}
}

func TestDevicesView_RefreshKeepsSelectedDeviceNotRow(t *testing.T) {
	bp := &bootProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "a", Name: "a", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
		{ID: "b", Name: "b", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
		{ID: "c", Name: "c", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
	}}}
	a := New("t", bp)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 4 })
	var first string
	onUI(a, func() { d, _ := dv.selected(); first = d.ID })
	if first != "c" {
		t.Fatalf("first render should select the top row (c sorts first by name desc), got %q", first)
	}
	// pick b, then make c boot so the order changes; the highlight must follow b
	onUI(a, func() { dv.table.Select(2, 0) })
	bp.mu.Lock()
	bp.devices[2].State = device.StateBooted
	bp.mu.Unlock()
	onUI(a, func() { dv.Refresh() })
	waitFor(t, a, 5*time.Second, func() bool { return dv.devices[0].State == device.StateBooted })
	var after string
	onUI(a, func() { d, _ := dv.selected(); after = d.ID })
	if after != "b" {
		t.Errorf("selection should stay on device b after a re-sort, got %q", after)
	}
}

type hwProvider struct {
	fakeProvider
	mu    sync.Mutex
	saved device.Hardware
}

func (h *hwProvider) Hardware(context.Context, device.Device) (device.Hardware, error) {
	return device.Hardware{RAMMB: 2048, Cores: 4, DiskGB: 6}, nil
}

func (h *hwProvider) SetHardware(_ context.Context, _ device.Device, hw device.Hardware) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saved = hw
	return nil
}

func TestDevicesView_EditHardware(t *testing.T) {
	hp := &hwProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
	}}}
	a := New("t", hp)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })

	onUI(a, func() { dv.onKey(tcell.NewEventKey(tcell.KeyRune, 'e', tcell.ModNone)) })
	waitFor(t, a, 5*time.Second, func() bool { _, ok := a.top().(*hardwareView); return ok })
	onUI(a, func() {
		hv := a.top().(*hardwareView)
		if got := hv.form.GetFormItem(0).(*tview.InputField).GetText(); got != "2048" {
			t.Errorf("ram prefilled = %q", got)
		}
		hv.form.GetFormItem(0).(*tview.InputField).SetText("4096")
		hv.form.GetFormItem(2).(*tview.InputField).SetText("16")
		hv.form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	})
	waitFor(t, a, 5*time.Second, func() bool {
		hp.mu.Lock()
		defer hp.mu.Unlock()
		return hp.saved == (device.Hardware{RAMMB: 4096, Cores: 4, DiskGB: 16})
	})
}

func TestCreateView_HardwareFieldsOnlyForEditors(t *testing.T) {
	plain := New("t", &fakeProvider{platform: device.PlatformIOS})
	_, stopPlain := runHeadless(t, plain)
	defer stopPlain()
	var items int
	onUI(plain, func() {
		cv := newCreateView(plain, plain.providers[device.PlatformIOS], device.Image{Name: "iOS 26.5"}, []device.DeviceType{{ID: "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro", Name: "iPhone 17 Pro", Screen: "1206x2622 @3x"}})
		items = cv.form.GetFormItemCount()
		label := cv.form.GetFormItem(1).(*tview.InputField).GetText()
		if !strings.Contains(label, "iPhone 17 Pro (iPhone-17-Pro)") || !strings.Contains(label, "1206x2622 @3x") {
			t.Errorf("device type label = %q", label)
		}
	})
	if items != 2 {
		t.Errorf("ios form items = %d, want 2 (no hardware fields)", items)
	}

	hw := New("t", &hwProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid}})
	_, stopHW := runHeadless(t, hw)
	defer stopHW()
	onUI(hw, func() {
		cv := newCreateView(hw, hw.providers[device.PlatformAndroid], device.Image{Name: "img"}, nil)
		items = cv.form.GetFormItemCount()
	})
	if items != 5 {
		t.Errorf("android form items = %d, want 5 (name, type, ram, cores, disk)", items)
	}
}

func TestReadHardwareFields_RejectsGarbage(t *testing.T) {
	form := tview.NewForm()
	addHardwareFields(form, device.Hardware{RAMMB: 2048, Cores: 4, DiskGB: 6})
	form.GetFormItem(1).(*tview.InputField).SetText("many")
	if _, err := readHardwareFields(form, 0); err == nil || !strings.Contains(err.Error(), "cpu cores") {
		t.Errorf("expected a cpu cores error, got %v", err)
	}
	form.GetFormItem(1).(*tview.InputField).SetText("")
	hw, err := readHardwareFields(form, 0)
	if err != nil || hw != (device.Hardware{RAMMB: 2048, Cores: 0, DiskGB: 6}) {
		t.Errorf("blank means unchanged: %+v %v", hw, err)
	}
}

func TestDeviceTypeLabels_AlignScreens(t *testing.T) {
	labels := deviceTypeLabels([]device.DeviceType{
		{ID: "pixel_7", Name: "pixel_7", Screen: "1080x2400"},
		{ID: "pixel_9_pro_xl", Name: "pixel_9_pro_xl", Screen: "1344x2992"},
		{ID: "pixel_fold", Name: "pixel_fold"},
	})
	if strings.Index(labels[0], "1080x2400") != strings.Index(labels[1], "1344x2992") {
		t.Errorf("screen columns not aligned:\n%q\n%q", labels[0], labels[1])
	}
	if labels[2] != "pixel_fold" {
		t.Errorf("a type without a screen should not carry trailing padding: %q", labels[2])
	}
}

func TestDeviceTypeFilterAndResolve(t *testing.T) {
	types := []device.DeviceType{
		{ID: "pixel_7", Name: "pixel_7", Screen: "1080x2400"},
		{ID: "pixel_9_pro", Name: "pixel_9_pro", Screen: "1280x2856"},
		{ID: "pixel_9_pro_xl", Name: "pixel_9_pro_xl", Screen: "1344x2992"},
		{ID: "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro", Name: "iPhone 17 Pro", Screen: "1206x2622 @3x"},
	}
	labels := deviceTypeLabels(types)
	if got := filterLabels(labels, labels[1]); len(got) != len(labels) {
		t.Errorf("an exact label should keep the whole list: %v", got)
	}
	if got := filterLabels(labels, "9 pro"); len(got) != 2 {
		t.Errorf("filter '9 pro' = %v", got)
	}
	if got := filterLabels(labels, "IPHONE"); len(got) != 1 {
		t.Errorf("filter is case-insensitive: %v", got)
	}
	if id, err := resolveDeviceType(types, labels, labels[1]); err != nil || id != "pixel_9_pro" {
		t.Errorf("exact label -> %q %v", id, err)
	}
	if id, err := resolveDeviceType(types, labels, " "+labels[1]+" "); err != nil || id != "pixel_9_pro" {
		t.Errorf("surrounding whitespace is ignored -> %q %v", id, err)
	}
	if id, err := resolveDeviceType(types, labels, "pro_xl"); err != nil || id != "pixel_9_pro_xl" {
		t.Errorf("unique substring -> %q %v", id, err)
	}
	if _, err := resolveDeviceType(types, labels, "pixel"); err == nil {
		t.Error("ambiguous text must not resolve")
	}
	if _, err := resolveDeviceType(types, labels, "galaxy"); err == nil {
		t.Error("unknown text must not resolve")
	}
}

func TestDevicesView_HidesNeverUsedSimulators(t *testing.T) {
	now := time.Now()
	a := New("t", &fakeProvider{platform: device.PlatformIOS, devices: []device.Device{
		{ID: "used", Name: "iPhone 17 Pro", Platform: device.PlatformIOS, Kind: device.KindVirtual, State: device.StateShutdown, LastActiveAt: now.Add(-time.Hour)},
		{ID: "fresh1", Name: "iPhone 16", Platform: device.PlatformIOS, Kind: device.KindVirtual, State: device.StateShutdown},
		{ID: "fresh2", Name: "iPad mini", Platform: device.PlatformIOS, Kind: device.KindVirtual, State: device.StateShutdown},
		{ID: "phone", Name: "my iPhone", Platform: device.PlatformIOS, Kind: device.KindPhysical, State: device.StateOffline},
	}})
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return len(dv.devices) == 4 })
	var rows int
	var title string
	onUI(a, func() { rows, title = dv.table.GetRowCount(), dv.table.GetTitle() })
	if rows != 3 || !strings.Contains(title, "+2 unused sims") {
		t.Errorf("default should hide the 2 never-booted sims: rows=%d title=%q", rows, title)
	}
	onUI(a, func() {
		dv.onKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
		rows = dv.table.GetRowCount()
	})
	if rows != 5 {
		t.Errorf("s should reveal them: rows=%d", rows)
	}
}

func TestImagesView_InstalledOnlyByDefault(t *testing.T) {
	a := New("t", &fakeProvider{platform: device.PlatformAndroid})
	_, stop := runHeadless(t, a)
	defer stop()
	var rows int
	var title string
	onUI(a, func() {
		iv := newImagesView(a)
		iv.rows = []imageRow{
			{platform: device.PlatformAndroid, image: device.Image{ID: "a", Name: "a", Version: "36", Installed: true}},
			{platform: device.PlatformAndroid, image: device.Image{ID: "b", Name: "b", Version: "35"}},
			{platform: device.PlatformAndroid, image: device.Image{ID: "c", Name: "c", Version: "34"}},
		}
		iv.render()
		rows, title = iv.table.GetRowCount(), iv.table.GetTitle()
		iv.onKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone))
		if iv.table.GetRowCount() != 4 {
			t.Errorf("s should show downloadable images too, rows=%d", iv.table.GetRowCount())
		}
	})
	if rows != 2 || !strings.Contains(title, "+2 downloadable") {
		t.Errorf("default should list installed only: rows=%d title=%q", rows, title)
	}
}

func TestLogsView_RedrawKeepsScrollPosition(t *testing.T) {
	a := New("test", &fakeProvider{platform: device.PlatformAndroid})
	screen, stop := runHeadless(t, a)
	defer stop()
	var lv *logsView
	onUI(a, func() {
		screen.SetSize(120, 30)
		a.tv.Sync()
		lv = newLogsView(a, device.Device{Name: "dev", State: device.StateBooted}, nil)
		a.push(lv)
		lines := make([]string, 0, 300)
		for i := range 300 {
			lines = append(lines, fmt.Sprintf("line %03d", i))
		}
		lv.append(lines)
	})
	a.tv.QueueUpdateDraw(func() {})
	var before, after, atEndAfter int
	onUI(a, func() {
		lv.text.ScrollTo(40, 0)
	})
	a.tv.QueueUpdateDraw(func() {})
	onUI(a, func() {
		before, _ = lv.text.GetScrollOffset()
		lv.onKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone)) // toggles wrap -> redraw
	})
	a.tv.QueueUpdateDraw(func() {})
	onUI(a, func() { after, _ = lv.text.GetScrollOffset() })
	if before != 40 || after != 40 {
		t.Errorf("scroll offset before=%d after=%d, want both 40", before, after)
	}
	onUI(a, func() { lv.text.ScrollToEnd() })
	a.tv.QueueUpdateDraw(func() {})
	onUI(a, func() {
		lv.onKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	})
	a.tv.QueueUpdateDraw(func() {})
	onUI(a, func() { atEndAfter, _ = lv.text.GetScrollOffset() })
	if atEndAfter <= 200 {
		t.Errorf("a view at the end should stay at the end after redraw, offset=%d", atEndAfter)
	}
}

func TestDangerNote_PhysicalDelete(t *testing.T) {
	iphone := dangerNote("delete device", device.Device{Kind: device.KindPhysical, Platform: device.PlatformIOS})
	if !strings.Contains(iphone, "pairing") {
		t.Errorf("ios physical note should talk about pairing: %q", iphone)
	}
	android := dangerNote("delete device", device.Device{Kind: device.KindPhysical, Platform: device.PlatformAndroid})
	if !strings.Contains(android, "adb connection") {
		t.Errorf("android physical note should talk about the adb connection: %q", android)
	}
	avd := dangerNote("delete device", device.Device{Kind: device.KindVirtual})
	if !strings.Contains(avd, "removed for good") {
		t.Errorf("virtual note = %q", avd)
	}
}

func TestStatus_LongErrorWraps(t *testing.T) {
	a := New("t", &fakeProvider{platform: device.PlatformAndroid})
	screen, stop := runHeadless(t, a)
	defer stop()
	onUI(a, func() { screen.SetSize(80, 30); a.tv.Sync() })
	a.tv.QueueUpdateDraw(func() {})
	long := strings.Repeat("adb -s emulator-5554 shell pm install failed: ", 4)
	onUI(a, func() { a.flashErr(fmt.Errorf("%s", long)) })
	a.tv.QueueUpdateDraw(func() {})
	var rows, height int
	onUI(a, func() {
		rows = a.statusRows
		_, _, _, height = a.status.GetRect()
	})
	if rows < 3 || height != rows {
		t.Errorf("status rows=%d height=%d for a %d-char message on an 80-col screen", rows, height, len(long))
	}
	onUI(a, func() { a.setStatus("") })
	onUI(a, func() { rows = a.statusRows })
	if rows != 1 {
		t.Errorf("clearing should shrink the status back to one row, got %d", rows)
	}
}

func TestSpinner_RunsWhileBusyAndClears(t *testing.T) {
	slow := &fakeProvider{platform: device.PlatformAndroid, delay: 700 * time.Millisecond, devices: []device.Device{
		{ID: "a", Name: "a", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateShutdown},
	}}
	a := New("t", slow)
	_, stop := runHeadless(t, a)
	defer stop()
	time.Sleep(350 * time.Millisecond)
	var text string
	var rows int
	onUI(a, func() { text, rows = a.status.GetText(true), a.statusRows })
	if !strings.Contains(text, ">_ >_") || !strings.Contains(text, "loading devices") || rows < 4 {
		t.Errorf("runner should be showing with the caption: rows=%d text=%q", rows, text)
	}
	var f1, f2 int
	onUI(a, func() { f1 = a.spinFrame })
	time.Sleep(400 * time.Millisecond)
	onUI(a, func() { f2 = a.spinFrame })
	if f1 == f2 {
		t.Error("runner frame did not advance")
	}
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 && a.busy == 0 })
	onUI(a, func() { text, rows = a.status.GetText(true), a.statusRows })
	if strings.Contains(text, ">_ >_") || rows != 1 {
		t.Errorf("runner should be gone after loading: rows=%d text=%q", rows, text)
	}
}

func TestRenderRunner_FramesDiffer(t *testing.T) {
	if renderRunner(0, "x") == renderRunner(1, "x") {
		t.Error("consecutive frames must differ, otherwise nothing runs")
	}
	for i := range runnerFrames {
		if got := len(runnerFrames[i]); got != 4 {
			t.Errorf("frame %d has %d rows, want 4", i, got)
		}
	}
}

type logProvider struct {
	fakeProvider
	script string
}

func (p *logProvider) LogCmd(ctx context.Context, _ device.Device, _ *device.App) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "sh", "-c", p.script), nil
}

func TestLogsView_RunnerUntilFirstLine(t *testing.T) {
	p := &logProvider{fakeProvider: fakeProvider{platform: device.PlatformAndroid}, script: "sleep 0.4; echo hello; sleep 10"}
	a := New("test", p)
	_, stop := runHeadless(t, a)
	defer stop()
	var lv *logsView
	var busy int
	onUI(a, func() {
		lv = newLogsView(a, device.Device{Name: "dev", Platform: device.PlatformAndroid, State: device.StateBooted}, nil)
		a.push(lv)
		busy = a.busy
	})
	if busy == 0 {
		t.Fatal("the runner should show while the stream is silent")
	}
	waitFor(t, a, 5*time.Second, func() bool { return a.busy == 0 && strings.Contains(lv.text.GetText(true), "hello") })
	onUI(a, func() { lv.stop() })
}

func TestCreateView_FiltersIncompatibleTypesAndNames(t *testing.T) {
	a := New("t", &fakeProvider{platform: device.PlatformIOS})
	_, stop := runHeadless(t, a)
	defer stop()
	types := []device.DeviceType{
		{ID: "com.apple.CoreSimulator.SimDeviceType.iPhone-6s-Plus", Name: "iPhone 6s Plus", MinRuntime: "9.0", MaxRuntime: "15.255.255", Family: "iPhone"},
		{ID: "com.apple.CoreSimulator.SimDeviceType.iPhone-15", Name: "iPhone 15", MinRuntime: "17.0", MaxRuntime: "26.255.255", Family: "iPhone"},
		{ID: "com.apple.CoreSimulator.SimDeviceType.Apple-TV-4K-4K", Name: "Apple TV 4K", MinRuntime: "9.0", MaxRuntime: "65535.255.255", Family: "Apple TV"},
	}
	var name, typ string
	var all []string
	onUI(a, func() {
		cv := newCreateView(a, a.providers[device.PlatformIOS], device.Image{Name: "iOS 17.5", Version: "17.5", Platform: "iOS"}, types)
		name = cv.form.GetFormItem(0).(*tview.InputField).GetText()
		typ = cv.form.GetFormItem(1).(*tview.InputField).GetText()
		all = cv.labels
	})
	if name != "iOS_17.5" {
		t.Errorf("default name = %q, want iOS_17.5 (version not doubled)", name)
	}
	if !strings.Contains(typ, "iPhone 15") || strings.Contains(typ, "6s") {
		t.Errorf("default type should be a compatible one: %q", typ)
	}
	if len(all) != 1 || strings.Contains(strings.Join(all, ""), "Apple TV") {
		t.Errorf("an iOS runtime should list phones and pads only: %v", all)
	}
}

func TestCreateView_ListOpensOnEnter(t *testing.T) {
	a := New("t", &fakeProvider{platform: device.PlatformIOS})
	_, stop := runHeadless(t, a)
	defer stop()
	types := []device.DeviceType{{ID: "x.a", Name: "iPhone A", Family: "iPhone"}, {ID: "x.b", Name: "iPhone B", Family: "iPhone"}}
	var cv *createView
	onUI(a, func() {
		cv = newCreateView(a, a.providers[device.PlatformIOS], device.Image{Name: "iOS 26.5", Version: "26.5", Platform: "iOS"}, types)
		a.push(cv)
		cv.form.SetFocus(1)
		a.tv.SetFocus(cv.form)
	})
	// the form's input capture runs first, then whatever it lets through reaches the field
	press := func(k tcell.Key) {
		onUI(a, func() {
			if ev := cv.onKey(tcell.NewEventKey(k, 0, tcell.ModNone)); ev != nil {
				cv.form.InputHandler()(ev, func(tview.Primitive) {})
			}
		})
	}
	state := func() (open bool, item, button int, text string) {
		onUI(a, func() {
			open = cv.listOpen
			item, button = cv.form.GetFocusedItemIndex()
			text = cv.completion.GetText()
		})
		return
	}
	if open, _, _, _ := state(); open {
		t.Fatal("the list must stay closed until enter")
	}
	press(tcell.KeyEnter)
	if open, item, _, _ := state(); !open || item != 1 {
		t.Fatalf("enter should open the list on the device type field, open=%v item=%d", open, item)
	}
	press(tcell.KeyDown)
	if open, item, _, text := state(); !open || item != 1 || !strings.HasPrefix(text, "iPhone A") {
		t.Errorf("down inside the open list moves the highlight only: open=%v item=%d text=%q", open, item, text)
	}
	press(tcell.KeyEnter)
	if open, _, _, text := state(); open || !strings.HasPrefix(text, "iPhone B") {
		t.Errorf("enter should pick the highlighted entry and close: open=%v text=%q", open, text)
	}
	onUI(a, func() {
		if ev := cv.onKey(tcell.NewEventKey(tcell.KeyRune, 'A', tcell.ModNone)); ev != nil {
			cv.form.InputHandler()(ev, func(tview.Primitive) {})
		}
	})
	if open, _, _, text := state(); !open || text != "A" {
		t.Errorf("typing on a picked value should start a fresh search: open=%v text=%q", open, text)
	}
	press(tcell.KeyEscape)
	if open, item, _, _ := state(); open || item != 1 {
		t.Errorf("esc should close the list and stay on the field: open=%v item=%d", open, item)
	}
	press(tcell.KeyDown)
	if _, _, button, _ := state(); button != 0 {
		t.Errorf("with the list closed, down should reach the create button, got button=%d", button)
	}
}

func TestCreateView_ListHighlightVisible(t *testing.T) {
	a := New("t", &fakeProvider{platform: device.PlatformIOS})
	screen, stop := runHeadless(t, a)
	defer stop()
	var types []device.DeviceType
	for i := 0; i < 5; i++ {
		types = append(types, device.DeviceType{ID: fmt.Sprintf("com.apple.CoreSimulator.SimDeviceType.iPhone-%d", i), Name: fmt.Sprintf("iPhone %d", i), Screen: "1000x2000 @3x", Family: "iPhone"})
	}
	onUI(a, func() {
		cv := newCreateView(a, a.providers[device.PlatformIOS], device.Image{Name: "iOS 26.5", Version: "26.5", Platform: "iOS"}, types)
		a.push(cv)
		cv.form.SetFocus(1)
		a.tv.SetFocus(cv.form)
		cv.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	})
	a.tv.QueueUpdateDraw(func() {})
	_, wantBG, _ := focusStyle.Decompose()
	waitFor(t, a, 3*time.Second, func() bool {
		w, h := screen.Size()
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				_, _, st, _ := screen.GetContent(x, y)
				if _, bg, _ := st.Decompose(); bg == wantBG {
					return true
				}
			}
		}
		return false
	})
}

type createProvider struct {
	fakeProvider
	mu      sync.Mutex
	created []string
	booted  []string
}

func (p *createProvider) Create(_ context.Context, name string, _ device.Image, _ string, _ *device.Hardware) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, name)
	p.devices = append(p.devices, device.Device{ID: "new-" + name, Name: name, Platform: p.platform, Kind: device.KindVirtual, State: device.StateShutdown})
	return nil
}

func (p *createProvider) List(context.Context) ([]device.Device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]device.Device(nil), p.devices...), nil
}

func (p *createProvider) Boot(_ context.Context, d device.Device) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.booted = append(p.booted, d.ID)
	for i := range p.devices {
		if p.devices[i].ID == d.ID {
			p.devices[i].State = device.StateBooted
		}
	}
	return nil
}

func TestCreateView_CreateBootsAndSelectsTheNewDevice(t *testing.T) {
	p := &createProvider{fakeProvider: fakeProvider{platform: device.PlatformIOS, devices: []device.Device{
		{ID: "old", Name: "old", Platform: device.PlatformIOS, Kind: device.KindVirtual, State: device.StateBooted, LastActiveAt: time.Now()},
	}}}
	a := New("t", p)
	_, stop := runHeadless(t, a)
	defer stop()
	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return len(dv.devices) == 1 })
	types := []device.DeviceType{{ID: "x.a", Name: "iPhone A", Family: "iPhone"}}
	onUI(a, func() {
		a.push(newImagesView(a))
		cv := newCreateView(a, a.providers[device.PlatformIOS], device.Image{ID: "rt", Name: "iOS 26.5", Version: "26.5", Platform: "iOS"}, types)
		a.push(cv)
		cv.form.SetFocus(cv.form.GetFormItemCount()) // create button
		cv.form.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	})
	waitFor(t, a, 5*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.booted) == 1 && len(a.stack) == 1 && a.busy == 0
	})
	p.mu.Lock()
	booted := append([]string(nil), p.booted...)
	p.mu.Unlock()
	if booted[0] != "new-iOS_26.5" {
		t.Errorf("the new device should boot right after creation, booted %v", booted)
	}
	waitFor(t, a, 5*time.Second, func() bool {
		d, ok := dv.selected()
		return ok && d.ID == "new-iOS_26.5"
	})
	var status string
	onUI(a, func() { status = a.status.GetText(true) })
	if !strings.Contains(status, "created iOS_26.5") {
		t.Errorf("the created flash should survive the device list refresh, status = %q", status)
	}
}

package ui

import (
	"context"
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
func (f *fakeProvider) InstallApp(context.Context, device.Device, string) error    { return nil }
func (f *fakeProvider) UninstallApp(context.Context, device.Device, string) error  { return nil }
func (f *fakeProvider) LaunchApp(context.Context, device.Device, string) error     { return nil }
func (f *fakeProvider) LogCmd(context.Context, device.Device) (*exec.Cmd, error)   { return nil, nil }
func (f *fakeProvider) Images(context.Context) ([]device.Image, error)             { return nil, nil }
func (f *fakeProvider) InstallImage(context.Context, device.Image) error           { return nil }
func (f *fakeProvider) Create(context.Context, string, device.Image, string) error { return nil }
func (f *fakeProvider) DeviceTypes(context.Context) ([]string, error)              { return nil, nil }

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
		{ID: "udid1", Name: "iPhone 17", Platform: device.PlatformIOS, Kind: device.KindVirtual, Transport: device.TransportSim, Runtime: "iOS 26.4", State: device.StateShutdown},
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
		cv = newCreateView(a, a.providers[device.PlatformAndroid], device.Image{ID: "img", Name: "img", Version: "1"}, []string{"pixel_7", "pixel_8"})
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
		t.Errorf("down: button = %d, want 0 (create)", button)
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
	lines := strings.Split(strings.TrimRight(renderHints(hints), "\n"), "\n")
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
		prim.(*tview.Modal).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
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

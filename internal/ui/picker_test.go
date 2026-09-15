package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/siner308/sims/internal/device"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "build", "outputs"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "Runner.app"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "old.apk"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "new.apk"), []byte("xx"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644))
	must(os.Chtimes(filepath.Join(dir, "old.apk"), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)))
	return dir
}

func TestPicker_ListsDirsThenNewestInstallables(t *testing.T) {
	dir := fixtureDir(t)
	a := New("test", &fakeProvider{platform: device.PlatformAndroid})
	_, stop := runHeadless(t, a)
	defer stop()

	var picked string
	v := newPickerView(a, []string{".apk"}, func(p string) { picked = p })
	v.dir = dir
	a.tv.QueueUpdate(func() { a.push(v) })

	var names []string
	a.tv.QueueUpdate(func() {
		for r := 1; r < v.table.GetRowCount(); r++ {
			names = append(names, v.table.GetCell(r, 0).Text)
		}
	})
	want := []string{"build/", "Runner.app/", "new.apk", "old.apk"}
	if len(names) != len(want) {
		t.Fatalf("rows = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, names[i], want[i])
		}
	}

	// enter on a directory descends, backspace returns
	a.tv.QueueUpdate(func() {
		v.table.Select(1, 0)
		v.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	})
	if v.dir != filepath.Join(dir, "build") {
		t.Errorf("enter on build/ left dir = %q", v.dir)
	}
	a.tv.QueueUpdate(func() { v.onKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone)) })
	if v.dir != dir {
		t.Errorf("backspace left dir = %q", v.dir)
	}

	// enter on a file picks it and pops the view
	a.tv.QueueUpdate(func() {
		v.table.Select(3, 0)
		v.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	})
	if picked != filepath.Join(dir, "new.apk") {
		t.Errorf("picked = %q", picked)
	}
	var stillTop bool
	onUI(a, func() { stillTop = a.top() == v })
	if stillTop {
		t.Error("picker still on the stack after picking")
	}
}

func TestPicker_AppBundleIsAFileForIOS(t *testing.T) {
	dir := fixtureDir(t)
	a := New("test", &fakeProvider{platform: device.PlatformIOS})
	_, stop := runHeadless(t, a)
	defer stop()

	var picked string
	v := newPickerView(a, []string{".app"}, func(p string) { picked = p })
	v.dir = dir
	a.tv.QueueUpdate(func() {
		a.push(v)
		v.table.Select(2, 0)
		v.onKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	})
	if picked != filepath.Join(dir, "Runner.app") {
		t.Errorf("picked = %q, want the .app bundle", picked)
	}
}

func TestCompletePath(t *testing.T) {
	dir := fixtureDir(t)
	got := completePath(filepath.Join(dir, "bu"))
	if len(got) != 1 || got[0] != filepath.Join(dir, "build")+string(filepath.Separator) {
		t.Errorf("completePath(bu) = %v", got)
	}
	got = completePath(dir + string(filepath.Separator))
	for _, g := range got {
		if filepath.Base(g) == ".hidden" {
			t.Error("hidden entries should not complete from an empty prefix")
		}
	}
}

func TestInstallableExts(t *testing.T) {
	cases := []struct {
		d    device.Device
		want string
	}{
		{device.Device{Platform: device.PlatformAndroid, Kind: device.KindVirtual}, ".apk"},
		{device.Device{Platform: device.PlatformAndroid, Kind: device.KindPhysical}, ".apk"},
		{device.Device{Platform: device.PlatformIOS, Kind: device.KindVirtual}, ".app"},
		{device.Device{Platform: device.PlatformIOS, Kind: device.KindPhysical}, ".app"},
	}
	for _, c := range cases {
		if got := installableExts(c.d)[0]; got != c.want {
			t.Errorf("%s/%s first ext = %q, want %q", c.d.Platform, c.d.Kind, got, c.want)
		}
	}
}

func TestPickAndInstall_FallsBackToTUIPicker(t *testing.T) {
	linux := "linux"
	prev := dialogOS.Swap(&linux)
	t.Cleanup(func() { dialogOS.Store(prev) })

	fp := &fakeProvider{platform: device.PlatformAndroid, devices: []device.Device{
		{ID: "avd1", Name: "Pixel_7", Platform: device.PlatformAndroid, Kind: device.KindVirtual, State: device.StateBooted, Serial: "emulator-5554"},
	}}
	a := New("test", fp)
	_, stop := runHeadless(t, a)
	defer stop()

	dv := a.stack[0].(*devicesView)
	waitFor(t, a, 5*time.Second, func() bool { return dv.table.GetRowCount() == 2 })
	var av *appsView
	a.tv.QueueUpdate(func() { dv.openApps(); av = a.top().(*appsView) })
	a.tv.QueueUpdate(func() { av.pickAndInstall(fp, true) })
	waitFor(t, a, 5*time.Second, func() bool { _, ok := a.top().(*pickerView); return ok })
}

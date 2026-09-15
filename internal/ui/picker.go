package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type pickerView struct {
	app        *App
	table      *tview.Table
	dir        string
	exts       []string
	filter     string
	showHidden bool
	onPick     func(path string)
	now        func() time.Time
}

type pickerEntry struct {
	name  string
	path  string
	isDir bool
	size  int64
	mod   time.Time
}

func newPickerView(a *App, exts []string, onPick func(string)) *pickerView {
	v := &pickerView{app: a, table: newTable(), exts: exts, onPick: onPick, now: time.Now, dir: startDir()}
	v.table.SetInputCapture(v.onKey)
	return v
}

func startDir() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	home, _ := os.UserHomeDir()
	return home
}

func (v *pickerView) Name() string               { return "pick" }
func (v *pickerView) Primitive() tview.Primitive { return v.table }
func (v *pickerView) Hints() []hint {
	return []hint{
		{"enter", "open dir / pick file"}, {"backspace", "parent dir"}, {"~", "home"}, {"d", "downloads"},
		{".", "toggle hidden"}, {"t", "type a path"}, {"/", "filter"},
	}
}

func (v *pickerView) Refresh() { v.render() }

func (v *pickerView) installable(name string, isDir bool) bool {
	for _, ext := range v.exts {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}

func (v *pickerView) entries() ([]pickerEntry, error) {
	dirents, err := os.ReadDir(v.dir)
	if err != nil {
		return nil, err
	}
	var out []pickerEntry
	for _, de := range dirents {
		name := de.Name()
		if !v.showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		// a .app bundle is a directory on disk but the unit simctl installs, so it is listed as a file
		isDir := de.IsDir() && !v.installable(name, true)
		if !isDir && !v.installable(name, false) {
			continue
		}
		if v.filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(v.filter)) {
			continue
		}
		out = append(out, pickerEntry{name: name, path: filepath.Join(v.dir, name), isDir: isDir, size: info.Size(), mod: info.ModTime()})
	}
	slices.SortFunc(out, func(a, b pickerEntry) int {
		if a.isDir != b.isDir {
			if a.isDir {
				return -1
			}
			return 1
		}
		if a.isDir {
			return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
		}
		return b.mod.Compare(a.mod)
	})
	return out, nil
}

func (v *pickerView) render() {
	v.table.Clear()
	setHeader(v.table, "NAME", "SIZE", "MODIFIED")
	entries, err := v.entries()
	if err != nil {
		v.app.flashErr(err)
	}
	r := 1
	for _, e := range entries {
		name, size := e.name, humanBytes(uint64(e.size))
		color := tcell.ColorDefault
		if e.isDir {
			name, size, color = name+"/", "", tcell.ColorAqua
		}
		v.table.SetCell(r, 0, tview.NewTableCell(highlight(name, v.filter)).SetTextColor(color).SetReference(e))
		v.table.SetCell(r, 1, tview.NewTableCell(size).SetAlign(tview.AlignRight).SetTextColor(tcell.ColorGray))
		v.table.SetCell(r, 2, tview.NewTableCell(relativeTime(e.mod, v.now())).SetTextColor(tcell.ColorGray))
		r++
	}
	if r == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell(fmt.Sprintf("[gray]no %s here[-]", strings.Join(v.exts, "/"))).SetSelectable(false))
	}
	v.table.Select(1, 0)
	title := fmt.Sprintf(" pick %s in %s [%d] ", strings.Join(v.exts, "/"), tview.Escape(shortenHome(v.dir)), r-1)
	if v.filter != "" {
		title += fmt.Sprintf("/%s ", v.filter)
	}
	v.table.SetTitle(title)
}

func shortenHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (v *pickerView) selected() (pickerEntry, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return pickerEntry{}, false
	}
	e, ok := cell.GetReference().(pickerEntry)
	return e, ok
}

func (v *pickerView) cd(dir string) {
	v.dir = dir
	v.filter = ""
	v.render()
}

func (v *pickerView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEnter:
		e, ok := v.selected()
		if !ok {
			return nil
		}
		if e.isDir {
			v.cd(e.path)
			return nil
		}
		v.app.pop()
		v.onPick(e.path)
		return nil
	case tcell.KeyBackspace, tcell.KeyBackspace2, tcell.KeyLeft:
		v.cd(filepath.Dir(v.dir))
		return nil
	case tcell.KeyRight:
		if e, ok := v.selected(); ok && e.isDir {
			v.cd(e.path)
		}
		return nil
	}
	switch ev.Rune() {
	case '~':
		if home, err := os.UserHomeDir(); err == nil {
			v.cd(home)
		}
	case 'd':
		if home, err := os.UserHomeDir(); err == nil {
			v.cd(filepath.Join(home, "Downloads"))
		}
	case '.':
		v.showHidden = !v.showHidden
		v.render()
	case '/':
		v.app.prompt("filter:", "", func(s string) { v.filter = s; v.render() })
	case 't':
		v.app.promptPath("path:", v.dir+string(filepath.Separator), func(p string) {
			p = expandHome(p)
			if info, err := os.Stat(p); err == nil && info.IsDir() && !v.installable(p, true) {
				v.cd(p)
				return
			}
			v.app.pop()
			v.onPick(p)
		})
	default:
		return ev
	}
	return nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func completePath(typed string) []string {
	dir, prefix := filepath.Split(expandHome(typed))
	if dir == "" {
		dir = "."
	}
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range dirents {
		name := de.Name()
		if !strings.HasPrefix(name, prefix) || (prefix == "" && strings.HasPrefix(name, ".")) {
			continue
		}
		full := filepath.Join(dir, name)
		if de.IsDir() {
			full += string(filepath.Separator)
		}
		out = append(out, full)
	}
	return out
}

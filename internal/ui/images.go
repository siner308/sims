package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/siner308/sims/internal/device"
)

type imageRow struct {
	platform device.Platform
	image    device.Image
}

type imagesView struct {
	app     *App
	table   *tview.Table
	rows    []imageRow
	filter  string
	showAll bool
}

func newImagesView(a *App) *imagesView {
	v := &imagesView{app: a, table: newTable()}
	v.table.SetTitle(" images ")
	v.table.SetInputCapture(v.onKey)
	return v
}

func (v *imagesView) Name() string               { return "images" }
func (v *imagesView) Primitive() tview.Primitive { return v.table }
func (v *imagesView) Hints() []hint {
	return []hint{{"enter", "new device"}, {"i", "install image"}, {"s", "show downloadable"}, {"/", "filter"}}
}

func (v *imagesView) Refresh() {
	v.app.setStatus(" loading images...")
	var rows []imageRow
	var errs []string
	v.app.async(func() error {
		for _, p := range v.app.providers {
			imgs, err := p.Images(v.app.ctx)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			for _, img := range imgs {
				rows = append(rows, imageRow{platform: p.Platform(), image: img})
			}
		}
		return nil
	}, func() {
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if a.image.Installed != b.image.Installed {
				return a.image.Installed
			}
			if a.platform != b.platform {
				return a.platform < b.platform
			}
			if a.image.Version != b.image.Version {
				return a.image.Version > b.image.Version
			}
			return a.image.Name < b.image.Name
		})
		v.rows = rows
		v.render()
		if len(errs) > 0 {
			v.app.flashErr(fmt.Errorf("%s", strings.Join(errs, "; ")))
		} else {
			v.app.setStatus("")
		}
	})
}

func (v *imagesView) render() {
	v.table.Clear()
	setHeader(v.table, "PLATFORM", "VERSION", "NAME", "INSTALLED", "ID")
	r, hidden := 1, 0
	for _, row := range v.rows {
		if !v.showAll && !row.image.Installed {
			hidden++
			continue
		}
		if v.filter != "" && !strings.Contains(strings.ToLower(row.image.ID+row.image.Name), strings.ToLower(v.filter)) {
			continue
		}
		installed := "[gray]no[-]"
		if row.image.Installed {
			installed = "[green]yes[-]"
		}
		v.table.SetCell(r, 0, tview.NewTableCell(string(row.platform)).SetReference(row))
		v.table.SetCell(r, 1, tview.NewTableCell(row.image.Version))
		v.table.SetCell(r, 2, tview.NewTableCell(highlight(row.image.Name, v.filter)))
		v.table.SetCell(r, 3, tview.NewTableCell(installed))
		v.table.SetCell(r, 4, tview.NewTableCell(highlight(row.image.ID, v.filter)).SetTextColor(tcell.ColorGray))
		r++
	}
	if r == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell("[gray]no installed images; press s to see what can be downloaded[-]").SetSelectable(false))
	}
	v.table.Select(1, 0)
	title := " images "
	if v.filter != "" {
		title = fmt.Sprintf(" images /%s ", v.filter)
	}
	title += fmt.Sprintf("[%d] ", r-1)
	if hidden > 0 {
		title += fmt.Sprintf("[gray]+%d downloadable (s)[-] ", hidden)
	}
	v.table.SetTitle(title)
}

func (v *imagesView) selected() (imageRow, bool) {
	row, _ := v.table.GetSelection()
	cell := v.table.GetCell(row, 0)
	if cell == nil {
		return imageRow{}, false
	}
	ir, ok := cell.GetReference().(imageRow)
	return ir, ok
}

func (v *imagesView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	switch {
	case ev.Rune() == '/':
		v.app.prompt("filter:", "", func(s string) { v.filter = s; v.render() })
	case ev.Rune() == 's':
		v.showAll = !v.showAll
		v.render()
	case ev.Rune() == 'i':
		row, ok := v.selected()
		if !ok {
			return nil
		}
		if row.image.Installed {
			v.app.flash("already installed")
			return nil
		}
		p := v.app.providers[row.platform]
		v.app.confirm(fmt.Sprintf("install %s?", row.image.ID), func() {
			v.app.setStatus(" installing " + row.image.ID + " (this can take minutes)...")
			v.app.async(func() error { return p.InstallImage(v.app.ctx, row.image) }, func() {
				v.app.flash("installed " + row.image.ID)
				v.Refresh()
			})
		})
	case ev.Rune() == 'n', ev.Key() == tcell.KeyEnter:
		row, ok := v.selected()
		if !ok {
			return nil
		}
		if !row.image.Installed {
			v.app.flash("install the image first (<i>)")
			return nil
		}
		v.createFrom(row)
	default:
		return ev
	}
	return nil
}

func (v *imagesView) createFrom(row imageRow) {
	p := v.app.providers[row.platform]
	var types []device.DeviceType
	v.app.async(func() error {
		var err error
		types, err = p.DeviceTypes(v.app.ctx)
		return err
	}, func() {
		v.app.push(newCreateView(v.app, p, row.image, types))
	})
}

type createView struct {
	app        *App
	form       *tview.Form
	completion *tview.InputField
	labels     []string // one per compatible type, what the completion list shows
	// listOpen: the drop-down is out, so up/down/enter belong to it; closed, the arrows walk the form
	listOpen bool
}

// Android AVDs take RAM, cores and disk; simulators have no such knobs, so the form only shows them
// when the provider can write them back.
func newCreateView(a *App, p device.Provider, img device.Image, types []device.DeviceType) *createView {
	compatible := make([]device.DeviceType, 0, len(types))
	for _, t := range types {
		if t.Supports(img) {
			compatible = append(compatible, t)
		}
	}
	types = compatible
	v := &createView{app: a, form: tview.NewForm()}
	v.form.SetBorder(true).SetTitle(fmt.Sprintf(" new %s device from %s ", p.Platform(), img.Name)).SetTitleColor(titleColor())
	v.form.SetFieldStyle(fieldStyle).SetLabelColor(tcell.ColorYellow)
	v.form.SetButtonStyle(buttonStyle).SetButtonActivatedStyle(focusStyle)
	v.form.SetInputCapture(v.onKey)

	defaultName := img.Name
	if !strings.Contains(img.Name, img.Version) {
		defaultName += " " + img.Version
	}
	defaultName = strings.ReplaceAll(defaultName, " ", "_")
	labels := deviceTypeLabels(types)
	defaultType := ""
	for i, t := range types {
		if strings.Contains(strings.ToLower(t.ID), "pixel_7") || strings.HasSuffix(t.ID, "iPhone-17-Pro") {
			defaultType = labels[i]
		}
	}
	if defaultType == "" {
		// simctl lists phones newest first, then pads, TVs and watches; land on the newest phone
		for _, l := range labels {
			if strings.Contains(strings.ToLower(l), "iphone") {
				defaultType = l
				break
			}
		}
	}
	if defaultType == "" && len(labels) > 0 {
		defaultType = labels[0]
	}
	v.form.AddInputField("name", defaultName, 40, nil, nil)
	// the device type is a text field with a filtering completion list: typing "pixel 9" narrows the
	// options to those containing both words, and enter takes the highlighted one
	v.form.AddInputField("device type", defaultType, 60, nil, nil)
	typeField := v.form.GetFormItem(1).(*tview.InputField)
	// styles must be set first: tview builds the drop-down inside SetAutocompleteFunc (the default
	// text already matches a label) and only reads the styles at that moment
	typeField.SetAutocompleteStyles(tcell.ColorDefault, fieldStyle, focusStyle)
	typeField.SetAutocompleteFunc(func(current string) []string {
		if !v.listOpen {
			return nil
		}
		return filterLabels(labels, current)
	})
	typeField.SetAutocompletedFunc(func(text string, _ int, source int) bool {
		if source == tview.AutocompletedNavigate {
			return false // only the highlight moves; the text changes when an entry is picked
		}
		typeField.SetText(text)
		v.listOpen = false
		return true
	})
	v.completion = typeField
	v.labels = labels

	_, editable := p.(device.HardwareEditor)
	if editable {
		addHardwareFields(v.form, device.Hardware{RAMMB: 2048, Cores: 4, DiskGB: 6})
	}
	for i := range v.form.GetFormItemCount() {
		markFocus(v.form.GetFormItem(i))
	}
	v.form.AddButton("create", func() {
		name := v.form.GetFormItem(0).(*tview.InputField).GetText()
		deviceType, err := resolveDeviceType(types, labels, v.form.GetFormItem(1).(*tview.InputField).GetText())
		if err != nil {
			a.flashErr(err)
			return
		}
		var hw *device.Hardware
		if editable {
			h, err := readHardwareFields(v.form, 2)
			if err != nil {
				a.flashErr(err)
				return
			}
			hw = &h
		}
		a.setStatus(" creating and booting " + name + "...")
		var created device.Device
		a.async(func() error {
			if err := p.Create(a.ctx, name, img, deviceType, hw); err != nil {
				return err
			}
			d, err := findCreated(a.ctx, p, name)
			if err != nil {
				return err
			}
			created = d
			return p.Boot(a.ctx, d)
		}, func() {
			a.pop()
			a.pop()
			if dv, ok := a.top().(*devicesView); ok {
				dv.focusID = created.ID
			}
			a.flash("created " + name + ", booting")
		})
	})
	v.form.AddButton("cancel", func() { a.pop() })
	v.form.SetCancelFunc(func() { a.pop() })
	return v
}

// findCreated looks the new device up by name: Create returns no id, and a fresh simulator has no
// boot history, so among namesakes the never-booted one is the new one.
func findCreated(ctx context.Context, p device.Provider, name string) (device.Device, error) {
	devices, err := p.List(ctx)
	if err != nil {
		return device.Device{}, err
	}
	var found *device.Device
	for i := range devices {
		d := devices[i]
		if d.Name != name || d.Kind != device.KindVirtual {
			continue
		}
		if found == nil || (d.LastActiveAt.IsZero() && !found.LastActiveAt.IsZero()) {
			found = &d
		}
	}
	if found == nil {
		return device.Device{}, fmt.Errorf("%s was created but does not show up in the device list yet; refresh (r) and boot it with b", name)
	}
	return *found, nil
}

// filterLabels keeps the labels that contain every whitespace-separated word of the query, case-insensitively.
// A query that already is one of the labels gets the whole list back, so the drop-down stays open
// and up/down can move on to a neighbour instead of collapsing to the one entry.
func filterLabels(labels []string, query string) []string {
	for _, l := range labels {
		if strings.TrimSpace(l) == strings.TrimSpace(query) && query != "" {
			return labels
		}
	}
	words := strings.Fields(strings.ToLower(query))
	var out []string
	for _, l := range labels {
		lower := strings.ToLower(l)
		ok := true
		for _, w := range words {
			if !strings.Contains(lower, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

// resolveDeviceType maps what was typed back to a type id: an exact label first, then the single match.
func resolveDeviceType(types []device.DeviceType, labels []string, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return "", fmt.Errorf("pick a device type")
	}
	for i, l := range labels {
		if strings.TrimSpace(l) == typed {
			return types[i].ID, nil
		}
	}
	matches := filterLabels(labels, typed)
	if len(matches) == 1 {
		for i, l := range labels {
			if l == matches[0] {
				return types[i].ID, nil
			}
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no device type matches %q", typed)
	}
	return "", fmt.Errorf("%q matches %d device types; pick one from the list", typed, len(matches))
}

// deviceTypeLabels pads the name column to one width so the screen sizes line up in the dropdown.
func deviceTypeLabels(types []device.DeviceType) []string {
	names := make([]string, len(types))
	width := 0
	for i, t := range types {
		names[i] = t.Name
		if t.Name != t.ID && t.Name != "" {
			names[i] = fmt.Sprintf("%s (%s)", t.Name, shortTypeID(t.ID))
		}
		width = max(width, len(names[i]))
	}
	labels := make([]string, len(types))
	for i, t := range types {
		if t.Screen == "" {
			labels[i] = names[i]
			continue
		}
		labels[i] = fmt.Sprintf("%-*s  %s", width, names[i], t.Screen)
	}
	return labels
}

// simctl type ids carry a long reverse-DNS prefix that says nothing to the reader
func shortTypeID(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

func addHardwareFields(form *tview.Form, hw device.Hardware) {
	form.AddInputField("ram (MB)", strconv.Itoa(hw.RAMMB), 10, tview.InputFieldInteger, nil)
	form.AddInputField("cpu cores", strconv.Itoa(hw.Cores), 10, tview.InputFieldInteger, nil)
	form.AddInputField("disk (GB)", strconv.Itoa(hw.DiskGB), 10, tview.InputFieldInteger, nil)
}

func readHardwareFields(form *tview.Form, first int) (device.Hardware, error) {
	read := func(i int, what string) (int, error) {
		text := strings.TrimSpace(form.GetFormItem(i).(*tview.InputField).GetText())
		if text == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s must be a whole number", what)
		}
		return n, nil
	}
	var hw device.Hardware
	var err error
	if hw.RAMMB, err = read(first, "ram"); err != nil {
		return hw, err
	}
	if hw.Cores, err = read(first+1, "cpu cores"); err != nil {
		return hw, err
	}
	if hw.DiskGB, err = read(first+2, "disk"); err != nil {
		return hw, err
	}
	return hw, nil
}

// Arrow keys move between fields and buttons; tview's Form only walks with tab and enter.
// An open dropdown keeps up/down for its own list.
func (v *createView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	if item, _ := v.form.GetFocusedItemIndex(); item >= 0 && v.form.GetFormItem(item) == tview.FormItem(v.completion) {
		switch key := ev.Key(); {
		case v.listOpen && (key == tcell.KeyUp || key == tcell.KeyDown):
			return ev
		case v.listOpen && key == tcell.KeyEscape:
			v.listOpen = false // tview drops the list itself; esc does not leave the form
			return ev
		case v.listOpen && (key == tcell.KeyEnter || key == tcell.KeyTab):
			if len(filterLabels(v.labels, v.completion.GetText())) == 0 {
				v.listOpen = false // nothing to pick, let the key walk the form instead
				break
			}
			return ev
		case !v.listOpen && key == tcell.KeyEnter:
			v.listOpen = true
			v.completion.Autocomplete()
			return nil
		case !v.listOpen && (key == tcell.KeyRune || key == tcell.KeyBackspace || key == tcell.KeyBackspace2):
			// typing starts a fresh search: appending to the picked label would match nothing
			v.completion.SetText("")
			v.listOpen = true
			return ev
		}
	}
	return formArrowKeys(v.app, v.form, ev)
}

func formArrowKeys(a *App, form *tview.Form, ev *tcell.EventKey) *tcell.EventKey {
	item, button := form.GetFocusedItemIndex()
	if item >= 0 {
		if dd, ok := form.GetFormItem(item).(*tview.DropDown); ok && dd.IsOpen() {
			return ev
		}
	}
	total := form.GetFormItemCount() + form.GetButtonCount()
	cur := item
	if button >= 0 {
		cur = form.GetFormItemCount() + button
	}
	switch ev.Key() {
	case tcell.KeyDown:
		form.SetFocus((cur + 1) % total)
	case tcell.KeyUp:
		form.SetFocus((cur - 1 + total) % total)
	case tcell.KeyRight:
		if button < 0 {
			return ev
		}
		form.SetFocus(form.GetFormItemCount() + (button+1)%form.GetButtonCount())
	case tcell.KeyLeft:
		if button < 0 {
			return ev
		}
		form.SetFocus(form.GetFormItemCount() + (button-1+form.GetButtonCount())%form.GetButtonCount())
	default:
		return ev
	}
	a.tv.SetFocus(form)
	return nil
}

func (v *createView) Name() string               { return "new" }
func (v *createView) Primitive() tview.Primitive { return v.form }
func (v *createView) Hints() []hint {
	return []hint{{"arrows", "move"}, {"enter", "open list / pick / press"}, {"type", "search device types"}, {"esc", "close list / cancel"}}
}
func (v *createView) Refresh() {}

// hardwareView edits RAM, cores and disk of an existing AVD; the emulator picks the values up at its next boot.
type hardwareView struct {
	app  *App
	form *tview.Form
}

func newHardwareView(a *App, editor device.HardwareEditor, d device.Device, current device.Hardware) *hardwareView {
	v := &hardwareView{app: a, form: tview.NewForm()}
	v.form.SetBorder(true).SetTitle(fmt.Sprintf(" hardware of %s ", d.Name)).SetTitleColor(titleColor())
	v.form.SetFieldStyle(fieldStyle).SetLabelColor(tcell.ColorYellow)
	v.form.SetButtonStyle(buttonStyle).SetButtonActivatedStyle(focusStyle)
	v.form.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey { return formArrowKeys(a, v.form, ev) })
	addHardwareFields(v.form, current)
	for i := range v.form.GetFormItemCount() {
		markFocus(v.form.GetFormItem(i))
	}
	v.form.AddButton("save", func() {
		hw, err := readHardwareFields(v.form, 0)
		if err != nil {
			a.flashErr(err)
			return
		}
		a.async(func() error { return editor.SetHardware(a.ctx, d, hw) }, func() {
			msg := "saved hardware of " + d.Name
			if d.Running() {
				msg += " (takes effect after the next boot)"
			}
			a.flash(msg)
			a.pop()
		})
	})
	v.form.AddButton("cancel", func() { a.pop() })
	v.form.SetCancelFunc(func() { a.pop() })
	return v
}

func (v *hardwareView) Name() string               { return "hardware" }
func (v *hardwareView) Primitive() tview.Primitive { return v.form }
func (v *hardwareView) Hints() []hint {
	return []hint{{"up/down", "field"}, {"left/right", "button"}, {"enter", "press"}, {"esc", "cancel"}}
}
func (v *hardwareView) Refresh() {}

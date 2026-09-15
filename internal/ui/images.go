package ui

import (
	"fmt"
	"sort"
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
	app    *App
	table  *tview.Table
	rows   []imageRow
	filter string
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
	return []hint{{"enter", "new device"}, {"i", "install image"}, {"/", "filter"}}
}

func (v *imagesView) Refresh() {
	v.app.status.SetText(" loading images...")
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
			v.app.status.SetText("")
		}
	})
}

func (v *imagesView) render() {
	v.table.Clear()
	setHeader(v.table, "PLATFORM", "VERSION", "NAME", "INSTALLED", "ID")
	r := 1
	for _, row := range v.rows {
		if v.filter != "" && !strings.Contains(strings.ToLower(row.image.ID+row.image.Name), strings.ToLower(v.filter)) {
			continue
		}
		installed := "[gray]no[-]"
		if row.image.Installed {
			installed = "[green]yes[-]"
		}
		v.table.SetCell(r, 0, tview.NewTableCell(string(row.platform)).SetReference(row))
		v.table.SetCell(r, 1, tview.NewTableCell(row.image.Version))
		v.table.SetCell(r, 2, tview.NewTableCell(row.image.Name))
		v.table.SetCell(r, 3, tview.NewTableCell(installed))
		v.table.SetCell(r, 4, tview.NewTableCell(row.image.ID).SetTextColor(tcell.ColorGray))
		r++
	}
	if r == 1 {
		v.table.SetCell(1, 0, tview.NewTableCell("[gray]no images[-]").SetSelectable(false))
	}
	v.table.Select(1, 0)
	title := " images "
	if v.filter != "" {
		title = fmt.Sprintf(" images /%s ", v.filter)
	}
	v.table.SetTitle(fmt.Sprintf("%s[%d] ", title, r-1))
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
		v.app.prompt("filter:", v.filter, func(s string) { v.filter = s; v.render() })
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
			v.app.status.SetText(" installing " + row.image.ID + " (this can take minutes)...")
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
	var types []string
	v.app.async(func() error {
		var err error
		types, err = p.DeviceTypes(v.app.ctx)
		return err
	}, func() {
		v.app.push(newCreateView(v.app, p, row.image, types))
	})
}

type createView struct {
	app  *App
	form *tview.Form
}

func newCreateView(a *App, p device.Provider, img device.Image, types []string) *createView {
	v := &createView{app: a, form: tview.NewForm()}
	v.form.SetBorder(true).SetTitle(fmt.Sprintf(" new %s device from %s ", p.Platform(), img.Name))
	v.form.SetFieldStyle(fieldStyle).SetLabelColor(tcell.ColorYellow)
	v.form.SetButtonStyle(buttonStyle).SetButtonActivatedStyle(focusStyle)
	v.form.SetInputCapture(v.onKey)

	defaultName := strings.ReplaceAll(fmt.Sprintf("%s %s", img.Name, img.Version), " ", "_")
	typeIdx := 0
	for i, t := range types {
		if strings.Contains(strings.ToLower(t), "pixel_7") || strings.HasSuffix(t, "iPhone-17-Pro") {
			typeIdx = i
			break
		}
	}
	v.form.AddInputField("name", defaultName, 40, nil, nil)
	v.form.AddDropDown("device type", types, typeIdx, nil)
	dropdown := v.form.GetFormItemByLabel("device type").(*tview.DropDown)
	dropdown.SetFocusedStyle(focusStyle).SetListStyles(fieldStyle, focusStyle)
	v.form.AddButton("create", func() {
		name := v.form.GetFormItemByLabel("name").(*tview.InputField).GetText()
		_, deviceType := v.form.GetFormItemByLabel("device type").(*tview.DropDown).GetCurrentOption()
		a.status.SetText(" creating " + name + "...")
		a.async(func() error { return p.Create(a.ctx, name, img, deviceType) }, func() {
			a.flash("created " + name)
			a.pop()
			a.pop()
		})
	})
	v.form.AddButton("cancel", func() { a.pop() })
	v.form.SetCancelFunc(func() { a.pop() })
	return v
}

// Arrow keys move between fields and buttons; tview's Form only walks with tab and enter.
// An open dropdown keeps up/down for its own list.
func (v *createView) onKey(ev *tcell.EventKey) *tcell.EventKey {
	item, button := v.form.GetFocusedItemIndex()
	if item >= 0 {
		if dd, ok := v.form.GetFormItem(item).(*tview.DropDown); ok && dd.IsOpen() {
			return ev
		}
	}
	total := v.form.GetFormItemCount() + v.form.GetButtonCount()
	cur := item
	if button >= 0 {
		cur = v.form.GetFormItemCount() + button
	}
	switch ev.Key() {
	case tcell.KeyDown:
		v.form.SetFocus((cur + 1) % total)
	case tcell.KeyUp:
		v.form.SetFocus((cur - 1 + total) % total)
	case tcell.KeyRight:
		if button < 0 {
			return ev
		}
		v.form.SetFocus(v.form.GetFormItemCount() + (button+1)%v.form.GetButtonCount())
	case tcell.KeyLeft:
		if button < 0 {
			return ev
		}
		v.form.SetFocus(v.form.GetFormItemCount() + (button-1+v.form.GetButtonCount())%v.form.GetButtonCount())
	default:
		return ev
	}
	v.app.tv.SetFocus(v.form)
	return nil
}

func (v *createView) Name() string               { return "new" }
func (v *createView) Primitive() tview.Primitive { return v.form }
func (v *createView) Hints() []hint {
	return []hint{{"up/down", "field"}, {"left/right", "button"}, {"enter", "open list / press"}, {"esc", "cancel"}}
}
func (v *createView) Refresh() {}

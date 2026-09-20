package ui

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/siner308/sims/internal/proxy"
)

// openExternally hands text to the tool the user already reads text with. A terminal table is a
// poor place to read a long JSON body: there is no search that survives a redraw, no folding, and
// no copying out. A pager or an editor has all three, and the user has already configured which.
//
// The TUI is suspended for the duration, so the tool owns the terminal, and resumes when it exits.
func (a *App) openExternally(title, body string, editor bool) {
	path, err := writeScratch(title, body)
	if err != nil {
		a.flashErr(err)
		return
	}
	name, args := viewerFor(editor)
	if name == "" {
		a.flashErr(fmt.Errorf("no %s is set; export PAGER or EDITOR", viewerVar(editor)))
		return
	}
	// Suspend blocks its caller for as long as the tool is open, and this runs from a key handler
	// on the UI goroutine: calling it there would freeze every redraw and keystroke behind the
	// pager, including the ones tview needs to hand the screen back cleanly.
	go func() {
		a.tv.Suspend(func() {
			cmd := exec.Command(name, append(args, path)...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "sims: %s: %v\n", name, err)
			}
		})
		a.tv.QueueUpdateDraw(func() { a.flash("read in " + name + "; the file is at " + path) })
	}()
}

// viewerFor is the tool to open with, and the arguments it needs.
func viewerFor(editor bool) (string, []string) {
	if editor {
		if v := firstWord(os.Getenv("VISUAL")); v != "" {
			return v, nil
		}
		if v := firstWord(os.Getenv("EDITOR")); v != "" {
			return v, nil
		}
		return "", nil
	}
	if v := os.Getenv("PAGER"); v != "" {
		fields := strings.Fields(v)
		return fields[0], pagerFlags(fields[0], fields[1:])
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less", pagerFlags("less", nil)
	}
	return "", nil
}

// pagerFlags adds what a pager needs to hand the screen back cleanly. sims already owns the
// terminal's alternate screen, so a pager that opens its own leaves the TUI drawn over and the
// keyboard somewhere neither of them expects. -X keeps less on the current screen, -R lets colour
// through and -F exits at once for something that already fits. A pager the user configured with
// its own flags is left alone.
func pagerFlags(name string, given []string) []string {
	if len(given) > 0 {
		return given
	}
	switch filepath.Base(name) {
	case "less":
		return []string{"-R", "-F", "-X"}
	case "more":
		return []string{"-e"}
	}
	return nil
}

func viewerVar(editor bool) string {
	if editor {
		return "EDITOR"
	}
	return "PAGER"
}

func firstWord(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// writeScratch puts the text somewhere the tool can open it, named after what it holds so a tab in
// an editor says which exchange it is.
func writeScratch(title, body string) (string, error) {
	f, err := os.CreateTemp("", "sims-"+sanitize(title)+"-*.txt")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// exchangeText is one exchange as plain text: everything the detail view shows, with no markup, so
// a pager or an editor gets something it can search and fold.
func exchangeText(f proxy.Flow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", f.Method, f.URL)
	if f.Status > 0 {
		text := f.StatusText
		if text == "" {
			text = http.StatusText(f.Status)
		}
		fmt.Fprintf(&b, "%d %s\n", f.Status, text)
	}
	fmt.Fprintf(&b, "started  %s\n", f.Start.Format("2006-01-02 15:04:05.000"))
	if f.Done {
		fmt.Fprintf(&b, "finished %s\ntook     %s\n", f.Start.Add(f.Duration).Format("2006-01-02 15:04:05.000"), f.Duration)
	}
	if f.Process != "" {
		fmt.Fprintf(&b, "from     %s\n", f.Process)
	}
	if f.Error != "" {
		fmt.Fprintf(&b, "error    %s\n", f.Error)
	}

	writeHalf(&b, "REQUEST", f.ReqHeader, f.ReqBody, f.ReqSize, f.ReqTruncated)
	if f.Kind == proxy.KindTunnel {
		b.WriteString("\n--- RESPONSE ---\n\nsims did not read this exchange: the app pins its certificate, or the device does not trust the sims CA.\n")
		return b.String()
	}
	writeHalf(&b, "RESPONSE", f.RespHeader, f.RespBody, f.RespSize, f.RespTruncated)
	return b.String()
}

func writeHalf(b *strings.Builder, label string, header http.Header, body []byte, size int64, truncated bool) {
	fmt.Fprintf(b, "\n--- %s ---\n\n", label)
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range header[k] {
			fmt.Fprintf(b, "%s: %s\n", k, v)
		}
	}
	if size == 0 {
		b.WriteString("\n(no body)\n")
		return
	}
	fmt.Fprintf(b, "\nbody %s\n\n", proxy.SizeString(size))
	b.WriteString(proxy.Pretty(header, body))
	b.WriteString("\n")
	if truncated {
		fmt.Fprintf(b, "\n(sims kept the first %s of this body)\n", proxy.SizeString(int64(len(body))))
	}
}

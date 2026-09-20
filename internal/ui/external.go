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

// openInPager hands the text to $PAGER for folding and searching. The TUI is suspended for the
// duration, so the pager owns the terminal, and resumes when it exits.
//
// An editor goes through openInEditor instead: a terminal editor would need this same terminal, and
// a reader who then quits it is left looking at a screen sims no longer owns.
func (a *App) openInPager(title, body string) {
	path, err := writeScratch(title, body)
	if err != nil {
		a.flashErr(err)
		return
	}
	name, args := pagerCommand()
	if name == "" {
		a.flashErr(fmt.Errorf("no PAGER is set and less is not installed"))
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
		// Resume re-enters the alternate screen, which the terminal opens blank, while tcell still
		// holds what it last sent. An ordinary draw only writes cells it considers changed, so it
		// would send nothing and leave the screen as the tool left it. Sync redraws every cell.
		a.tv.Sync()
		a.tv.QueueUpdateDraw(func() { a.flash("read in " + name + "; the file is at " + path) })
	}()
}

// pagerCommand is the pager to open with, and the arguments it needs.
func pagerCommand() (string, []string) {
	if v := os.Getenv("PAGER"); v != "" {
		fields := strings.Fields(v)
		return fields[0], pagerFlags(fields[0], fields[1:])
	}
	if _, err := exec.LookPath("less"); err == nil {
		return "less", pagerFlags("less", nil)
	}
	return "", nil
}

// pagerFlags adds what a pager needs to be readable here. Suspending the TUI leaves the terminal's
// alternate screen first, so the pager is free to open its own and restore what was underneath on
// the way out: -X would keep it on the current screen instead and leave its output behind for the
// resumed TUI to draw over. -R lets colour through. A pager the user configured with its own flags
// is left alone.
func pagerFlags(name string, given []string) []string {
	if len(given) > 0 {
		return given
	}
	switch filepath.Base(name) {
	case "less":
		return []string{"-R"}
	}
	return nil
}

// openInEditor writes the text out and opens it in a windowed editor, chosen the first time and
// remembered after that. pick asks again, for a different editor or one installed since.
func (a *App) openInEditor(title, body string, pick bool) {
	path, err := writeScratch(title, body)
	if err != nil {
		a.flashErr(err)
		return
	}
	a.chooseEditor(path, pick)
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
	if f.Abandoned {
		fmt.Fprintf(&b, "note     the client closed the connection before the response finished, so the body below stops where it stopped reading\n")
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

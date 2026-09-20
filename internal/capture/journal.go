package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/siner308/sims/internal/device"
)

// journalDir holds one file per running capture, so a later run can undo what a killed one could
// not: SIGKILL, a panic and a power cut all skip Stop, and what they leave is a machine and a
// device pointing at a proxy that no longer exists.
//
// It is a directory rather than one file because several captures run at once. With a single path
// the second capture to start would overwrite the first one's record, and the first capture to stop
// would delete a record still needed by the others, in both cases leaving a changed machine with
// nothing on disk saying so.
const journalDir = "in-flight"

// journal is what has to be undone: it is written before the settings are changed and removed after
// they are put back, so its presence means a capture did not finish.
type journal struct {
	PID int `json:"pid"`
	// Service and Before are this machine's proxy settings as they were before the capture.
	Service string         `json:"service,omitempty"`
	Before  []journalProxy `json:"before,omitempty"`
	Device  *journalDevice `json:"device,omitempty"`
	Port    int            `json:"port"`
}

type journalProxy struct {
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Server  string `json:"server,omitempty"`
	Port    int    `json:"port,omitempty"`
}

// journalDevice is what a capture changed on the device, so a later run can clear it by id.
type journalDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Serial   string `json:"serial,omitempty"`
	Kind     string `json:"kind"`
}

// journalPath names one capture's record. The port makes it unique within a process, since two
// captures cannot listen on the same one.
func journalPath(dir string, pid, port int) string {
	return filepath.Join(dir, journalDir, fmt.Sprintf("%d-%d.json", pid, port))
}

func writeJournal(dir string, j journal) error {
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, journalDir), 0o700); err != nil {
		return err
	}
	return os.WriteFile(journalPath(dir, j.PID, j.Port), body, 0o600)
}

// removeJournal drops one capture's record and leaves every other capture's alone.
func removeJournal(dir string, pid, port int) {
	_ = os.Remove(journalPath(dir, pid, port))
}

// readJournals returns every record in the directory, newest first by file name.
func readJournals(dir string) []journal {
	entries, err := os.ReadDir(filepath.Join(dir, journalDir))
	if err != nil {
		return nil
	}
	var out []journal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, journalDir, e.Name()))
		if err != nil {
			continue
		}
		var j journal
		if err := json.Unmarshal(body, &j); err != nil {
			continue
		}
		out = append(out, j)
	}
	return out
}

// Leftover describes a capture that never cleaned up after itself.
type Leftover struct {
	// Machine is this Mac's proxy setting that is still in place, empty when none is.
	Machine string
	// Device names the device still pointing at the proxy, empty when none is.
	Device string
	// PID is the process that left it, for a message that says where it came from.
	PID int

	dir string
	j   journal
}

// Found reports whether there is anything to clean up.
func (l Leftover) Found() bool { return l.Machine != "" || l.Device != "" }

func (l Leftover) String() string {
	switch {
	case l.Machine != "" && l.Device != "":
		return fmt.Sprintf("a capture that did not stop cleanly left %s behind and %s pointing at it", l.Machine, l.Device)
	case l.Machine != "":
		return fmt.Sprintf("a capture that did not stop cleanly left %s behind", l.Machine)
	default:
		return fmt.Sprintf("a capture that did not stop cleanly left %s pointing at a proxy that is gone", l.Device)
	}
}

// FindLeftover looks for a capture that was killed before it could put things back: SIGKILL, a
// panic and a power cut all skip Stop. A journal whose process is still running belongs to a live
// capture and is left alone.
func FindLeftover(dir string) Leftover {
	if dir == "" {
		var err error
		if dir, err = DefaultCertDir(); err != nil {
			return Leftover{}
		}
	}
	for _, j := range readJournals(dir) {
		// a record whose process is still running belongs to a live capture, which will clean up
		// after itself
		if j.PID > 0 && processAlive(j.PID) {
			continue
		}
		l := Leftover{PID: j.PID, dir: dir, j: j}
		if j.Service != "" && hostProxyStillSet(j) {
			l.Machine = "this Mac's web proxy"
		}
		if j.Device != nil {
			l.Device = j.Device.Name
		}
		if l.Found() {
			return l
		}
		// nothing of this one is still in place, so its record is finished with
		removeJournal(dir, j.PID, j.Port)
	}
	return Leftover{}
}

// processAlive reports whether a pid is still running, so a live capture's journal is not undone.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// on unix FindProcess always succeeds, so signal 0 is the liveness check
	return p.Signal(sigZero) == nil
}

// Clean puts back whatever the killed capture left: this machine's proxy settings, and the setting
// on the device when a way to clear it is given. The journal goes even when part of it fails, so a
// device that is no longer attached does not keep the warning on screen for ever.
func (l Leftover) Clean(ctx context.Context, clearDevice func(context.Context, device.Device) error) error {
	var errs []error
	if l.j.Service != "" {
		if err := restoreRecordedProxy(l.j); err != nil {
			errs = append(errs, err)
		}
	}
	if d := l.j.Device; d != nil && clearDevice != nil {
		err := clearDevice(ctx, device.Device{
			ID: d.ID, Name: d.Name, Serial: d.Serial,
			Platform: device.Platform(d.Platform), Kind: device.Kind(d.Kind),
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("could not clear the proxy on %s: %w", d.Name, err))
		}
	}
	removeJournal(l.dir, l.j.PID, l.j.Port)
	return errors.Join(errs...)
}

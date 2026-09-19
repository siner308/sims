package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/siner308/sims/internal/device"
)

// journalName is the file a running capture leaves behind so a later run can undo what a killed one
// could not. SIGKILL, a panic or a power cut all skip Stop, and what they leave is a machine and a
// device pointing at a proxy that no longer exists.
const journalName = "in-flight.json"

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

func journalPath(dir string) string { return filepath.Join(dir, journalName) }

func writeJournal(dir string, j journal) error {
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(journalPath(dir), body, 0o600)
}

func removeJournal(dir string) {
	_ = os.Remove(journalPath(dir))
}

func readJournal(dir string) (journal, bool) {
	body, err := os.ReadFile(journalPath(dir))
	if err != nil {
		return journal{}, false
	}
	var j journal
	if err := json.Unmarshal(body, &j); err != nil {
		return journal{}, false
	}
	return j, true
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
	j, ok := readJournal(dir)
	if !ok {
		return Leftover{}
	}
	if j.PID > 0 && processAlive(j.PID) {
		return Leftover{}
	}
	l := Leftover{PID: j.PID, dir: dir, j: j}
	if j.Service != "" && hostProxyStillSet(j) {
		l.Machine = "this Mac's web proxy"
	}
	if j.Device != nil {
		l.Device = j.Device.Name
	}
	return l
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
	removeJournal(l.dir)
	return errors.Join(errs...)
}

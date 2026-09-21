package capture

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/siner308/sims/internal/device"
)

// A phone keeps the profile a capture gives it: iOS lets only its owner install or remove one, so the profile is made once, with a fixed port, and every later capture reuses it.
// The record is what the phone was given, which is how a later capture tells whether that profile still points here.
const phonesDir = "phones"

// PhonePort is where a phone's profile sends its traffic. It is fixed so the profile stays valid from one capture to the next.
const PhonePort = 9797

type PhoneRecord struct {
	Device          device.Device `json:"device"`
	Host            string        `json:"host"`
	Port            int           `json:"port"`
	SSID            string        `json:"ssid"`
	CertFingerprint string        `json:"certFingerprint"`
	InstalledAt     time.Time     `json:"installedAt"`
}

// Matches reports whether the profile the phone has would still send its traffic to this target and trust this certificate.
func (r PhoneRecord) Matches(host string, port int, ssid, fingerprint string) bool {
	return r.Host == host && r.Port == port && r.SSID == ssid && r.CertFingerprint == fingerprint
}

func isPhone(d device.Device) bool {
	return d.Platform == device.PlatformIOS && d.Kind == device.KindPhysical
}

func phonePath(dir, id string) string { return filepath.Join(dir, phonesDir, id+".json") }

// Phone is what an earlier capture gave the device, if anything.
func Phone(dir, id string) (PhoneRecord, bool) {
	body, err := os.ReadFile(phonePath(dir, id))
	if err != nil {
		return PhoneRecord{}, false
	}
	var r PhoneRecord
	if json.Unmarshal(body, &r) != nil || r.Port == 0 {
		return PhoneRecord{}, false
	}
	return r, true
}

func writePhone(dir string, r PhoneRecord) error {
	if err := os.MkdirAll(filepath.Join(dir, phonesDir), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(phonePath(dir, r.Device.ID), body, 0o600)
}

// ForgetPhone drops the record, so the next capture installs a profile again.
func ForgetPhone(dir, id string) error {
	err := os.Remove(phonePath(dir, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func PhoneRecords(dir string) []PhoneRecord {
	entries, err := os.ReadDir(filepath.Join(dir, phonesDir))
	if err != nil {
		return nil
	}
	var out []PhoneRecord
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if r, ok := Phone(dir, id); ok {
			out = append(out, r)
		}
	}
	return out
}

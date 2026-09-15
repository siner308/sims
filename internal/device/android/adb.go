package android

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/siner308/sims/internal/device"
)

type adbEntry struct {
	serial string
	state  string
	props  map[string]string
}

func parseADBDevices(out string) []adbEntry {
	var entries []adbEntry
	for _, line := range lines(out) {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		e := adbEntry{serial: fields[0], state: fields[1], props: map[string]string{}}
		for _, f := range fields[2:] {
			if k, v, ok := strings.Cut(f, ":"); ok {
				e.props[k] = v
			}
		}
		entries = append(entries, e)
	}
	return entries
}

func (e adbEntry) isEmulator() bool { return strings.HasPrefix(e.serial, "emulator-") }

// A wifi serial is either host:port or an mDNS service name ending in _adb-tls-connect._tcp.
func (e adbEntry) transport() device.Transport {
	if strings.Contains(e.serial, ":") || strings.Contains(e.serial, "_adb-tls-connect") {
		return device.TransportWiFi
	}
	return device.TransportUSB
}

func (e adbEntry) deviceState() device.State {
	switch e.state {
	case "device":
		return device.StateConnected
	case "offline":
		return device.StateOffline
	case "unauthorized":
		return device.StateUnauthorized
	}
	return device.StateUnknown
}

func (p *Provider) physicalDevices(ctx context.Context, entries []adbEntry) []device.Device {
	var devices []device.Device
	for _, e := range entries {
		if e.isEmulator() {
			continue
		}
		d := device.Device{
			ID:        e.serial,
			Serial:    e.serial,
			Name:      strings.ReplaceAll(e.props["model"], "_", " "),
			Model:     e.props["device"],
			Platform:  device.PlatformAndroid,
			Kind:      device.KindPhysical,
			Transport: e.transport(),
			State:     e.deviceState(),
		}
		if d.Name == "" {
			d.Name = e.serial
		}
		if d.State == device.StateConnected {
			if rel, err := run(ctx, p.sdk.adb(), "-s", e.serial, "shell", "getprop", "ro.build.version.release"); err == nil {
				d.Runtime = "Android " + strings.TrimSpace(rel)
			}
		}
		devices = append(devices, d)
	}
	return devices
}

func (p *Provider) Pair(ctx context.Context, addr, code string) error {
	args := []string{"pair", addr}
	if code != "" {
		args = append(args, code)
	}
	out, err := run(ctx, p.sdk.adb(), args...)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Successfully paired") {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

// adb connect exits 0 even on failure and reports it only in stdout.
func (p *Provider) Connect(ctx context.Context, addr string) error {
	out, err := run(ctx, p.sdk.adb(), "connect", addr)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "connected to") {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

func (p *Provider) Disconnect(ctx context.Context, d device.Device) error {
	if d.Transport != device.TransportWiFi {
		return errors.New("only wifi devices can be disconnected")
	}
	_, err := run(ctx, p.sdk.adb(), "disconnect", d.Serial)
	return err
}

func (p *Provider) EnableWireless(ctx context.Context, d device.Device) (string, error) {
	if d.Kind != device.KindPhysical || d.Transport != device.TransportUSB {
		return "", errors.New("plug the device in over USB first")
	}
	ip, err := p.deviceIP(ctx, d.Serial)
	if err != nil {
		return "", err
	}
	if _, err := run(ctx, p.sdk.adb(), "-s", d.Serial, "tcpip", "5555"); err != nil {
		return "", err
	}
	addr := ip + ":5555"
	return addr, p.Connect(ctx, addr)
}

func (p *Provider) deviceIP(ctx context.Context, serial string) (string, error) {
	out, err := run(ctx, p.sdk.adb(), "-s", serial, "shell", "ip", "-f", "inet", "addr", "show", "wlan0")
	if err != nil {
		return "", err
	}
	for _, f := range strings.Fields(out) {
		if ip, _, ok := strings.Cut(f, "/"); ok && strings.Count(ip, ".") == 3 {
			return ip, nil
		}
	}
	return "", fmt.Errorf("no wifi address on %s (is wifi on?)", serial)
}

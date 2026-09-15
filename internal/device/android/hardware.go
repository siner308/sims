package android

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/siner308/sims/internal/device"
)

func (p *Provider) Hardware(ctx context.Context, d device.Device) (device.Hardware, error) {
	if d.Kind != device.KindVirtual {
		return device.Hardware{}, errors.New("only AVDs have editable hardware")
	}
	raw, err := os.ReadFile(filepath.Join(avdHome(), d.ID+".avd", "config.ini"))
	if err != nil {
		return device.Hardware{}, err
	}
	return hardwareFromConfig(string(raw)), nil
}

// SetHardware writes only the fields that are set; the emulator reads them at the next boot.
func (p *Provider) SetHardware(ctx context.Context, d device.Device, hw device.Hardware) error {
	if d.Kind != device.KindVirtual {
		return errors.New("only AVDs have editable hardware")
	}
	values := map[string]string{}
	if hw.RAMMB > 0 {
		values["hw.ramSize"] = strconv.Itoa(hw.RAMMB)
	}
	if hw.Cores > 0 {
		values["hw.cpu.ncore"] = strconv.Itoa(hw.Cores)
	}
	if hw.DiskGB > 0 {
		values["disk.dataPartition.size"] = fmt.Sprintf("%dG", hw.DiskGB)
	}
	if len(values) == 0 {
		return nil
	}
	path := filepath.Join(avdHome(), d.ID+".avd", "config.ini")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, changed := applyConfig(string(raw), values)
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

func hardwareFromConfig(config string) device.Hardware {
	var hw device.Hardware
	for _, line := range strings.Split(config, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "hw.ramSize":
			hw.RAMMB = int(parseSize(val, 1<<20) >> 20)
		case "hw.cpu.ncore":
			hw.Cores, _ = strconv.Atoi(val)
		case "disk.dataPartition.size":
			hw.DiskGB = int(parseSize(val, 1) >> 30)
		}
	}
	return hw
}

// parseSize reads the emulator's mixed notation: a bare number in unit bytes (MB for RAM, bytes for disk)
// or a number with a K/M/G suffix.
func parseSize(val string, unit int64) int64 {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	mult := unit
	switch val[len(val)-1] {
	case 'K', 'k':
		mult, val = 1<<10, val[:len(val)-1]
	case 'M', 'm':
		mult, val = 1<<20, val[:len(val)-1]
	case 'G', 'g':
		mult, val = 1<<30, val[:len(val)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}

// screenFromSkin reads the first width/height pair, which sits under parts.device.display in every skin layout.
func screenFromSkin(layout string) string {
	var w, h string
	for _, line := range strings.Split(layout, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "width":
			if w == "" {
				w = f[1]
			}
		case "height":
			if h == "" {
				h = f[1]
			}
		}
		if w != "" && h != "" {
			return w + "x" + h
		}
	}
	return ""
}

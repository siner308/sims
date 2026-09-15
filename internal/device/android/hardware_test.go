package android

import (
	"testing"

	"github.com/siner308/sims/internal/device"
)

func TestHardwareFromConfig(t *testing.T) {
	cfg := "hw.ramSize = 2G\nhw.cpu.ncore = 4\ndisk.dataPartition.size = 6442450944\n"
	if got := hardwareFromConfig(cfg); got != (device.Hardware{RAMMB: 2048, Cores: 4, DiskGB: 6}) {
		t.Errorf("got %+v", got)
	}
	cfg = "hw.ramSize = 4096\ndisk.dataPartition.size = 16G\n"
	if got := hardwareFromConfig(cfg); got != (device.Hardware{RAMMB: 4096, DiskGB: 16}) {
		t.Errorf("got %+v", got)
	}
}

func TestScreenFromSkin(t *testing.T) {
	layout := "parts {\n  device {\n    display {\n      width 1080\n      height 2400\n    }\n  }\n  portrait {\n    width 1200\n    height 2541\n  }\n}\n"
	if got := screenFromSkin(layout); got != "1080x2400" {
		t.Errorf("got %q", got)
	}
	if got := screenFromSkin("nothing here"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestApplyConfig_Hardware(t *testing.T) {
	out, changed := applyConfig("hw.ramSize = 2G\nhw.cpu.ncore = 4\n", map[string]string{"hw.ramSize": "4096", "disk.dataPartition.size": "16G"})
	if !changed || out != "hw.ramSize = 4096\nhw.cpu.ncore = 4\ndisk.dataPartition.size = 16G\n" {
		t.Errorf("got %q", out)
	}
}

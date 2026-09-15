package android

import (
	"os"
	"path/filepath"
	"strings"
)

// avdmanager leaves every key at the emulator default. hw.keyboard = no is known to drop host keyboard
// input (no keyboard device appears in the guest); the other three are set to what the image and a
// typical phone would report, since their defaults describe a device with no GPU, no front camera and no Play.
func avdDefaults(config string) map[string]string {
	values := map[string]string{
		"hw.keyboard":     "yes",
		"hw.gpu.enabled":  "yes",
		"hw.camera.front": "emulated",
	}
	if isPlayImage(config) {
		values["PlayStore.enabled"] = "yes"
	}
	return values
}

// 16 KB page-size images list the Play tag only in tag.ids, so both keys are checked.
func isPlayImage(config string) bool {
	for _, line := range strings.Split(config, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "tag.id", "tag.ids":
			if strings.Contains(val, "google_apis_playstore") {
				return true
			}
		}
	}
	return false
}

func setAVDConfig(avdName string) error {
	path := filepath.Join(avdHome(), avdName+".avd", "config.ini")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, changed := applyConfig(string(raw), avdDefaults(string(raw)))
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

func applyConfig(content string, values map[string]string) (string, bool) {
	seen := map[string]bool{}
	changed := false
	ls := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i, line := range ls {
		key, _, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok {
			continue
		}
		if want, has := values[key]; has {
			seen[key] = true
			if next := key + " = " + want; next != line {
				ls[i] = next
				changed = true
			}
		}
	}
	for key, want := range values {
		if !seen[key] {
			ls = append(ls, key+" = "+want)
			changed = true
		}
	}
	return strings.Join(ls, "\n") + "\n", changed
}

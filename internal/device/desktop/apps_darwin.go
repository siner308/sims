package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/siner308/sims/internal/device"
)

// Apps lists what is running on this Mac, plus what is installed and not. A desktop's "apps" are
// what a reader needs to make sense of a capture: the row that says curl or Google Chrome is one of
// these, and seeing the list is how they know what else could be talking.
func (p *Provider) Apps(ctx context.Context, _ device.Device) ([]device.App, error) {
	running, err := runningApps(ctx)
	if err != nil {
		return nil, err
	}
	byBundle := map[string]device.App{}
	for _, a := range running {
		byBundle[a.BundleID] = a
	}
	for _, a := range installedApps(ctx) {
		if up, seen := byBundle[a.BundleID]; seen {
			// a running app keeps its state but takes the version the bundle knows
			if up.Version == "" && a.Version != "" {
				up.Version = a.Version
				byBundle[a.BundleID] = up
			}
			continue
		}
		byBundle[a.BundleID] = a
	}
	out := make([]device.App, 0, len(byBundle))
	for _, a := range byBundle {
		out = append(out, a)
	}
	// running first, then by name, which is the order every other platform's list uses
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// runningApps reads lsappinfo, which names every app the window server knows about, background
// agents included. Its output is a numbered entry per app followed by indented fields.
func runningApps(ctx context.Context) ([]device.App, error) {
	out, err := exec.CommandContext(ctx, "lsappinfo", "list").Output()
	if err != nil {
		return nil, err
	}
	var apps []device.App
	var cur device.App
	flush := func() {
		if cur.BundleID != "" || cur.Name != "" {
			apps = append(apps, cur)
		}
		cur = device.App{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, ") \"") && strings.Contains(trimmed, "ASN:"):
			flush()
			cur = device.App{Name: between(trimmed, `"`, `"`), Running: true, Source: "installed"}
		case strings.HasPrefix(trimmed, "bundleID="):
			cur.BundleID = strings.Trim(strings.TrimPrefix(trimmed, "bundleID="), `"`)
		case strings.HasPrefix(trimmed, "executable path="):
			path := strings.Trim(strings.TrimPrefix(trimmed, "executable path="), `"`)
			cur.Process = filepath.Base(path)
			cur.System = isSystemPath(path)
		case strings.HasPrefix(trimmed, "bundle path="):
			path := strings.Trim(strings.TrimPrefix(trimmed, "bundle path="), `"`)
			if !cur.System {
				cur.System = isSystemPath(path)
			}
			if _, version := bundleInfo(path); version != "" {
				cur.Version = version
			}
		}
	}
	flush()
	return apps, nil
}

// installedApps adds what is in the Applications folders but not running, so the list is what the
// machine has rather than only what it is doing this second.
func installedApps(ctx context.Context) []device.App {
	var apps []device.App
	for _, dir := range []string{"/Applications", "/Applications/Utilities", homeApplications()} {
		if dir == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(dir, "*.app"))
		if err != nil {
			continue
		}
		for _, bundle := range matches {
			name := strings.TrimSuffix(filepath.Base(bundle), ".app")
			id, version := bundleInfo(bundle)
			if id == "" {
				// an alias or a bundle whose Info.plist cannot be read still deserves a row; the
				// name is what a reader recognises, and a path in the id column is noise
				id = "app:" + name
			}
			apps = append(apps, device.App{
				BundleID: id,
				Name:     name,
				Version:  version,
				Process:  name,
				Source:   "installed",
				System:   isSystemPath(bundle),
			})
		}
	}
	return apps
}

// bundleInfo reads an app's identifier and version from its Info.plist. An alias to a volume that
// is not mounted, and anything else unreadable, comes back empty rather than as an error: the app
// is still on the machine and still belongs in the list.
func bundleInfo(bundle string) (id, version string) {
	body, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return "", ""
	}
	return plistString(body, "CFBundleIdentifier"), plistString(body, "CFBundleShortVersionString")
}

// plistString pulls one string value out of an Info.plist. Apple ships both the XML and the binary
// form, so the binary one is read by finding the key's text rather than by parsing the container.
func plistString(body []byte, key string) string {
	text := string(body)
	i := strings.Index(text, key)
	if i < 0 {
		return ""
	}
	rest := text[i+len(key):]
	// XML: <key>CFBundleIdentifier</key><string>com.example.app</string>
	if open := strings.Index(rest, "<string>"); open >= 0 && open < 64 {
		rest = rest[open+len("<string>"):]
		if end := strings.Index(rest, "</string>"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return ""
}

func homeApplications() string {
	home, err := homeDirOf()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Applications")
}

// isSystemPath marks what ships with macOS, which the apps view hides by default the way it hides a
// phone's preinstalled apps.
func isSystemPath(path string) bool {
	return strings.HasPrefix(path, "/System/") ||
		strings.HasPrefix(path, "/usr/") ||
		strings.HasPrefix(path, "/Library/Apple/") ||
		strings.Contains(path, "/Contents/Library/")
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

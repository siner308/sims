package android

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type apkInfo struct {
	Label   string `json:"label"`
	Version string `json:"version"`
}

// The install path carries a per-install hash, so it doubles as the cache key: a reinstall changes it.
type labelCache struct {
	mu   sync.Mutex
	path string
	data map[string]apkInfo
}

func newLabelCache() *labelCache {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	c := &labelCache{path: filepath.Join(dir, "sims", "apk-labels.json"), data: map[string]apkInfo{}}
	if raw, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(raw, &c.data)
	}
	return c
}

func (c *labelCache) get(key string) (apkInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

func (c *labelCache) put(key string, v apkInfo) {
	c.mu.Lock()
	c.data[key] = v
	raw, _ := json.Marshal(c.data)
	c.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(c.path), 0o755)
	_ = os.WriteFile(c.path, raw, 0o644)
}

var (
	labelRe   = regexp.MustCompile(`(?m)^application-label:'(.*)'$`)
	versionRe = regexp.MustCompile(`versionName='([^']*)'`)
)

func parseBadging(out string) apkInfo {
	var info apkInfo
	if m := labelRe.FindStringSubmatch(out); m != nil {
		info.Label = m[1]
	}
	if m := versionRe.FindStringSubmatch(out); m != nil {
		info.Version = m[1]
	}
	return info
}

func (s sdk) aapt2() (string, error) {
	matches, _ := filepath.Glob(filepath.Join(s.root, "build-tools", "*", exeName("aapt2")))
	if len(matches) == 0 {
		return "", errors.New("aapt2 not found under build-tools")
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

func exeName(name string) string {
	if isWindows {
		return name + ".exe"
	}
	return name
}

func (p *Provider) apkInfoFor(ctx context.Context, serial, apkPath string) (apkInfo, error) {
	if info, ok := p.labels.get(apkPath); ok {
		return info, nil
	}
	aapt, err := p.sdk.aapt2()
	if err != nil {
		return apkInfo{}, err
	}
	sum := sha1.Sum([]byte(serial + apkPath))
	local := filepath.Join(os.TempDir(), "sims-"+hex.EncodeToString(sum[:8])+".apk")
	defer os.Remove(local)
	if _, err := run(ctx, p.sdk.adb(), "-s", serial, "pull", apkPath, local); err != nil {
		return apkInfo{}, err
	}
	out, err := run(ctx, aapt, "dump", "badging", local)
	if err != nil {
		return apkInfo{}, err
	}
	info := parseBadging(out)
	if info.Label == "" {
		return apkInfo{}, errors.New("no application-label in badging")
	}
	p.labels.put(apkPath, info)
	return info, nil
}

func splitPackageLine(rest string) (apkPath, pkg, installer string) {
	rest, installer, _ = strings.Cut(rest, "  installer=")
	if i := strings.LastIndex(rest, "="); i >= 0 && strings.HasPrefix(rest, "/") {
		return rest[:i], strings.TrimSpace(rest[i+1:]), strings.TrimSpace(installer)
	}
	return "", strings.TrimSpace(rest), strings.TrimSpace(installer)
}

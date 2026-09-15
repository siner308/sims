// Package update finds the newest sims release on GitHub and swaps the running binary for it.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const Repo = "siner308/sims"

type Updater struct {
	// BaseURL is the repository page; releases hang off it as on github.com.
	BaseURL string
	Client  *http.Client
	OS      string
	Arch    string
}

func New() *Updater {
	return &Updater{
		BaseURL: "https://github.com/" + Repo,
		Client:  &http.Client{Timeout: 5 * time.Minute},
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}

// Latest resolves the tag behind releases/latest from the redirect alone, so it never touches the
// rate-limited API. The redirect is not followed: the tag is in the Location header.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.BaseURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	client := *u.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" || resp.StatusCode/100 != 3 {
		return "", fmt.Errorf("releases/latest returned %s without a redirect", resp.Status)
	}
	tag := path.Base(loc)
	if !strings.HasPrefix(tag, "v") || tag == "latest" {
		return "", fmt.Errorf("unexpected release location %q", loc)
	}
	return tag, nil
}

// Newer reports whether latest is a release after current. A dev build or anything that does not
// look like a version is never outdated, so nobody is nagged from a working tree.
func Newer(current, latest string) bool {
	cur, curPre, ok := parse(current)
	if !ok {
		return false
	}
	lat, latPre, ok := parse(latest)
	if !ok || latPre {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	// v0.1.2-0.20260915... (a go install of an untagged commit) sits before the v0.1.2 release
	return curPre
}

func parse(v string) (parts [3]int, pre bool, ok bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, pre = v[:i], true
	}
	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return parts, false, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return parts, false, false
		}
		parts[i] = n
	}
	return parts, pre, true
}

// ExecutablePath is the file to replace: the real binary behind any symlink, so a link in PATH
// keeps pointing at the updated file.
func ExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func (u *Updater) asset() string {
	if u.OS == "windows" {
		return fmt.Sprintf("sims_%s_%s.zip", u.OS, u.Arch)
	}
	return fmt.Sprintf("sims_%s_%s.tar.gz", u.OS, u.Arch)
}

// Apply downloads the release archive for tag, checks it against checksums.txt and writes the
// binary over target. The new file is renamed into place, so a failure anywhere leaves target as it was.
func (u *Updater) Apply(ctx context.Context, tag, target string) error {
	base := u.BaseURL + "/releases/download/" + tag + "/"
	asset := u.asset()
	archive, err := u.get(ctx, base+asset)
	if err != nil {
		return err
	}
	sums, err := u.get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	if err := verify(archive, sums, asset); err != nil {
		return err
	}
	bin, err := extract(archive, asset, u.OS == "windows")
	if err != nil {
		return err
	}
	return replace(target, bin)
}

func (u *Updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func verify(archive, sums []byte, asset string) error {
	got := sha256.Sum256(archive)
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			if fields[0] != hex.EncodeToString(got[:]) {
				return fmt.Errorf("checksum mismatch for %s", asset)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not listed in checksums.txt", asset)
}

func extract(archive []byte, asset string, windows bool) ([]byte, error) {
	name := "sims"
	if windows {
		name = "sims.exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s has no %s", asset, name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s has no %s", asset, name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
			return io.ReadAll(tr)
		}
	}
}

// Windows refuses to rename over a running executable but lets it be renamed away, so the old
// binary is moved aside first; elsewhere the rename swaps the inode under the running process.
func replace(target string, bin []byte) error {
	tmp := target + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: %s is not writable; rerun with sudo or reinstall with install.sh", err, filepath.Dir(target))
		}
		return err
	}
	old := ""
	if runtime.GOOS == "windows" {
		old = target + ".old"
		_ = os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		if old != "" {
			_ = os.Rename(old, target)
		}
		return err
	}
	return nil
}

// Run is `sims update`: it says where the binary stands against the latest release and, unless
// checkOnly, brings it up to date in place.
func Run(ctx context.Context, current string, checkOnly bool, w io.Writer) error {
	u := New()
	latest, err := u.Latest(ctx)
	if err != nil {
		return fmt.Errorf("could not check for updates: %w", err)
	}
	if !Newer(current, latest) {
		fmt.Fprintf(w, "sims %s is up to date (latest release %s)\n", current, latest)
		return nil
	}
	target, err := ExecutablePath()
	if err != nil {
		return err
	}
	if checkOnly {
		fmt.Fprintf(w, "sims %s is available (installed %s at %s); run: sims update\n", latest, current, target)
		return nil
	}
	fmt.Fprintf(w, "updating sims %s -> %s at %s\n", current, latest, target)
	if err := u.Apply(ctx, latest, target); err != nil {
		return err
	}
	fmt.Fprintf(w, "updated to %s\n", latest)
	return nil
}

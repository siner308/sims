package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.1", "v0.1.2", true},
		{"v0.1.2", "v0.1.2", false},
		{"v0.2.0", "v0.1.9", false},
		{"v0.9.9", "v1.0.0", true},
		{"v0.1.2-0.20260915120000-abcdef123456", "v0.1.2", true},
		{"v0.1.1", "v0.1.2-rc1", false},
		{"dev", "v0.1.2", false},
		{"(devel)", "v0.1.2", false},
		{"v0.1", "v0.1.2", false},
		{"v0.1.1", "latest", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestLatest_FromRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "https://github.com/siner308/sims/releases/tag/v0.1.2", http.StatusFound)
	}))
	defer srv.Close()
	u := &Updater{BaseURL: srv.URL, Client: srv.Client()}
	got, err := u.Latest(t.Context())
	if err != nil || got != "v0.1.2" {
		t.Fatalf("Latest = %q, %v", got, err)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func releaseServer(t *testing.T, archive []byte, sumLine string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/download/v0.1.2/sims_linux_amd64.tar.gz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/releases/download/v0.1.2/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(sumLine)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestApply_ReplacesBinary(t *testing.T) {
	archive := tarGz(t, map[string]string{"sims": "#!/bin/sh\necho new\n", "README.md": "readme"})
	sum := sha256.Sum256(archive)
	srv := releaseServer(t, archive, "deadbeef  sims_darwin_arm64.tar.gz\n"+hex.EncodeToString(sum[:])+"  sims_linux_amd64.tar.gz\n")
	target := filepath.Join(t.TempDir(), "sims")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{BaseURL: srv.URL, Client: srv.Client(), OS: "linux", Arch: "amd64"}
	if err := u.Apply(t.Context(), "v0.1.2", target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "#!/bin/sh\necho new\n" {
		t.Fatalf("target = %q, %v", got, err)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("binary is not executable: %v", fi.Mode())
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
}

func TestApply_ChecksumMismatchKeepsOldBinary(t *testing.T) {
	archive := tarGz(t, map[string]string{"sims": "tampered"})
	srv := releaseServer(t, archive, "0000000000000000000000000000000000000000000000000000000000000000  sims_linux_amd64.tar.gz\n")
	target := filepath.Join(t.TempDir(), "sims")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{BaseURL: srv.URL, Client: srv.Client(), OS: "linux", Arch: "amd64"}
	err := u.Apply(t.Context(), "v0.1.2", target)
	if err == nil {
		t.Fatal("want a checksum error")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Errorf("target was changed to %q", got)
	}
}

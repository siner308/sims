package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedSkillHasFrontmatter(t *testing.T) {
	if !strings.HasPrefix(SimsCLI, "---\nname: "+Name+"\n") {
		t.Fatalf("SKILL.md should open with frontmatter naming %s; got %q", Name, SimsCLI[:40])
	}
}

func TestRoots_OnlyAgentsThatExist(t *testing.T) {
	home := t.TempDir()
	if got := Roots(home); len(got) != 0 {
		t.Fatalf("empty home should have no roots, got %v", got)
	}
	os.Mkdir(filepath.Join(home, ".claude"), 0o755)
	got := Roots(home)
	if len(got) != 1 || got[0].Dir != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("roots with ~/.claude: %v", got)
	}
	os.Mkdir(filepath.Join(home, ".agents"), 0o755)
	if got = Roots(home); len(got) != 2 {
		t.Fatalf("roots with ~/.claude and ~/.agents: %v", got)
	}
}

func TestInstall(t *testing.T) {
	root := t.TempDir()
	path, res, err := Install(root, false)
	if err != nil || res != Written {
		t.Fatalf("first install: %v %v", res, err)
	}
	if got, _ := os.ReadFile(path); string(got) != SimsCLI {
		t.Fatal("installed copy differs from the embedded skill")
	}
	if _, res, _ = Install(root, false); res != UpToDate {
		t.Fatalf("second install: %v", res)
	}

	os.WriteFile(path, []byte("stale"), 0o644)
	if _, res, _ = Install(root, true); res != Written {
		t.Fatalf("refresh of a stale copy: %v", res)
	}
	if _, res, _ = Install(t.TempDir(), true); res != Absent {
		t.Fatalf("refresh with no copy: %v", res)
	}
}

func TestInstall_LeavesASymlinkAlone(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	if err := os.Symlink(real, filepath.Join(root, Name)); err != nil {
		t.Skip("no symlinks here:", err)
	}
	_, res, err := Install(root, false)
	if err != nil || res != LeftAlone {
		t.Fatalf("symlinked skill dir: %v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(real, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("wrote through the symlink")
	}
}

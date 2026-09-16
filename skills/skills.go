// Package skills embeds the agent skill so an installed copy always matches the installed binary.
package skills

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed sims-cli/SKILL.md
var SimsCLI string

// Name is the directory an agent sees the skill under: <root>/sims-cli/SKILL.md.
const Name = "sims-cli"

type Root struct {
	Agent string
	Dir   string
}

// Roots requires the agent's own directory (~/.claude, ~/.agents) to exist, so sims never plants a tree for an agent the user does not run.
func Roots(home string) []Root {
	var roots []Root
	for _, a := range []struct{ agent, dir string }{
		{"Claude Code", ".claude"},
		{"Agent Skills (Codex and others)", ".agents"},
	} {
		marker := filepath.Join(home, a.dir)
		if st, err := os.Stat(marker); err == nil && st.IsDir() {
			roots = append(roots, Root{Agent: a.agent, Dir: filepath.Join(marker, "skills")})
		}
	}
	return roots
}

type Result string

const (
	Written  Result = "written"
	UpToDate Result = "up to date"
	// A symlinked skill directory is the user's own pointer (a checkout, a dotfiles repo), so writing through it would edit their files.
	LeftAlone Result = "symlink, left alone"
	Absent    Result = "absent"
)

// refresh rewrites only a copy that is already there: an update keeps what the user installed current and adds nothing.
func Install(root string, refresh bool) (string, Result, error) {
	dir := filepath.Join(root, Name)
	path := filepath.Join(dir, "SKILL.md")
	if st, err := os.Lstat(dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return path, LeftAlone, nil
	}
	cur, err := os.ReadFile(path)
	switch {
	case err == nil && string(cur) == SimsCLI:
		return path, UpToDate, nil
	case errors.Is(err, os.ErrNotExist) && refresh:
		return path, Absent, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return path, "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, "", err
	}
	if err := os.WriteFile(path, []byte(SimsCLI), 0o644); err != nil {
		return path, "", err
	}
	return path, Written, nil
}

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/siner308/sims/skills"
)

func (c *cli) skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the skill that teaches an AI coding agent this command line",
		Long:  "Print the skill (a SKILL.md) that teaches an AI coding agent this command line. `sims skill install` puts it where the agents on this machine look.",
		Args:  cobra.NoArgs,
		RunE: c.run(func(context.Context, []string) error {
			_, err := io.WriteString(c.Out, skills.SimsCLI)
			return err
		}),
	}
	cmd.AddCommand(c.skillInstallCmd())
	return cmd
}

func (c *cli) skillInstallCmd() *cobra.Command {
	var dir string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the skill for the agents set up on this machine",
		Long: "Install the skill for the agents set up on this machine: ~/.claude/skills when ~/.claude exists (Claude Code) and ~/.agents/skills when ~/.agents exists (Codex and other agents that read that directory).\n" +
			"--dir installs into one skills directory instead. A skill directory that is a symlink is left alone.",
		Args: cobra.NoArgs,
		RunE: c.run(func(context.Context, []string) error {
			var roots []skills.Root
			if dir != "" {
				roots = []skills.Root{{Agent: dir, Dir: dir}}
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				if roots = skills.Roots(home); len(roots) == 0 {
					return fmt.Errorf("no agent found under %s (~/.claude, ~/.agents); pass --dir <skills-dir>, or: sims skill > <skills-dir>/%s/SKILL.md", home, skills.Name)
				}
			}
			type installed struct {
				Agent  string `json:"agent"`
				Path   string `json:"path"`
				Result string `json:"result"`
			}
			var results []installed
			for _, r := range roots {
				path, res, err := skills.Install(r.Dir, refresh)
				if err != nil {
					return err
				}
				if res == skills.Absent {
					continue
				}
				results = append(results, installed{r.Agent, path, string(res)})
			}
			if c.json {
				return c.printJSON(results)
			}
			for _, r := range results {
				fmt.Fprintf(c.Out, "%s: %s (%s)\n", r.Agent, r.Result, r.Path)
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&dir, "dir", "", "skills directory to install into, instead of the agents found under HOME")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "only rewrite copies that are already installed")
	return cmd
}

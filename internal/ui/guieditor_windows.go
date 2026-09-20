package ui

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// askShell lists the applications Windows itself offers for a .txt file, from the registry's
// OpenWithProgids and the per-extension application list. Nothing about which editors exist is
// built in: an editor installed anywhere is offered as soon as Windows knows it opens text.
const askShell = `
$ErrorActionPreference = 'SilentlyContinue'
$out = @{}
$roots = @(
  'HKLM:\SOFTWARE\Classes\.txt\OpenWithProgids',
  'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\FileExts\.txt\OpenWithProgids'
)
$progids = @()
foreach ($r in $roots) { if (Test-Path $r) { $progids += (Get-Item $r).GetValueNames() } }
foreach ($p in ($progids | Sort-Object -Unique)) {
  $cmd = (Get-ItemProperty "HKLM:\SOFTWARE\Classes\$p\shell\open\command" -Name '(default)').'(default)'
  if (-not $cmd) { $cmd = (Get-ItemProperty "HKCU:\SOFTWARE\Classes\$p\shell\open\command" -Name '(default)').'(default)' }
  if ($cmd) {
    $name = (Get-ItemProperty "HKLM:\SOFTWARE\Classes\$p" -Name '(default)').'(default)'
    if (-not $name) { $name = $p }
    $out[$name] = $cmd
  }
}
$out | ConvertTo-Json -Compress
`

func guiEditors(string) []guiEditor {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", askShell).Output()
	if err != nil {
		return nil
	}
	var commands map[string]string
	if err := json.Unmarshal(out, &commands); err != nil {
		return nil
	}
	var found []guiEditor
	for name, command := range commands {
		exe := executableFromCommand(command)
		if exe == "" {
			continue
		}
		found = append(found, guiEditor{Name: name, Open: func(file string) error {
			return exec.Command(exe, file).Start()
		}})
	}
	return sortEditors(found)
}

// executableFromCommand takes the program out of a registry open command, which carries argument
// placeholders after it and quotes the path when it has spaces in it.
func executableFromCommand(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
		return ""
	}
	if i := strings.Index(command, " "); i >= 0 {
		return command[:i]
	}
	return command
}

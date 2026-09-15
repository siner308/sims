package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

var (
	errNativePickerUnsupported = errors.New("no native file dialog on " + runtime.GOOS)
	errNativePickerCancelled   = errors.New("cancelled")
	dialogOS                   = runtime.GOOS
)

func nativePick(ctx context.Context, exts []string) (string, error) {
	switch dialogOS {
	case "darwin":
		return pickDarwin(ctx, exts)
	case "windows":
		return pickWindows(ctx, exts)
	}
	return "", errNativePickerUnsupported
}

// choose file matches bundles by UTI, not extension, so .app needs the bundle type.
func pickDarwin(ctx context.Context, exts []string) (string, error) {
	types := make([]string, 0, len(exts))
	for _, ext := range exts {
		if ext == ".app" {
			types = append(types, `"com.apple.application-bundle"`)
			continue
		}
		types = append(types, fmt.Sprintf("%q", strings.TrimPrefix(ext, ".")))
	}
	// The sheet is attached to the terminal app and that app is re-activated afterwards, whether the
	// user picked or cancelled, so keyboard focus comes back to the TUI instead of staying with osascript.
	script := fmt.Sprintf(`set term to (path to frontmost application as text)
set result to ""
try
	tell application term
		set f to choose file of type {%s} with prompt "sims: pick a build to install (%s)"
	end tell
	set result to POSIX path of f
on error msg number n
	tell application term to activate
	error msg number n
end try
tell application term to activate
result`, strings.Join(types, ", "), strings.Join(exts, ", "))
	out, err := runDialog(ctx, "osascript", "-e", script)
	if err != nil {
		if strings.Contains(err.Error(), "User canceled") || strings.Contains(err.Error(), "-128") {
			return "", errNativePickerCancelled
		}
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSpace(out), "/"), nil
}

func pickWindows(ctx context.Context, exts []string) (string, error) {
	patterns := make([]string, 0, len(exts))
	for _, ext := range exts {
		patterns = append(patterns, "*"+ext)
	}
	filter := fmt.Sprintf("Installable (%s)|%s|All files (*.*)|*.*", strings.Join(patterns, ", "), strings.Join(patterns, ";"))
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = 'sims: pick a build to install'
$d.Filter = '%s'
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName } else { exit 3 }`, filter)
	out, err := runDialog(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return "", errNativePickerCancelled
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runDialog(ctx context.Context, bin string, args ...string) (string, error) {
	if _, err := exec.LookPath(bin); err != nil {
		return "", errNativePickerUnsupported
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

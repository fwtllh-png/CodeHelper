package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type directoryPicker func(context.Context, string) (string, bool, error)

type directoryPickerCommand struct {
	name string
	args []string
}

func nativeDirectoryPicker() directoryPicker {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("osascript"); err == nil {
			return pickNativeDirectory
		}
	case "windows":
		if _, err := exec.LookPath("powershell.exe"); err == nil {
			return pickNativeDirectory
		}
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return nil
		}
		for _, name := range []string{"zenity", "kdialog"} {
			if _, err := exec.LookPath(name); err == nil {
				return pickNativeDirectory
			}
		}
	}
	return nil
}

func pickNativeDirectory(
	ctx context.Context,
	initialPath string,
) (string, bool, error) {
	command, err := nativeDirectoryPickerCommand(runtime.GOOS, initialPath)
	if err != nil {
		return "", false, err
	}
	output, err := exec.CommandContext(ctx, command.name, command.args...).CombinedOutput()
	selected := strings.TrimSpace(string(output))
	if err != nil {
		if directoryPickerCancelled(runtime.GOOS, err, selected) {
			return "", true, nil
		}
		return "", false, fmt.Errorf(
			"native directory picker failed: %w: %s",
			err,
			selected,
		)
	}
	if selected == "" {
		return "", true, nil
	}
	return filepath.Clean(selected), false, nil
}

func nativeDirectoryPickerCommand(
	goos string,
	initialPath string,
) (directoryPickerCommand, error) {
	initialPath = filepath.Clean(initialPath)
	switch goos {
	case "darwin":
		const script = `on run argv
try
set selectedFolder to choose folder with prompt "Choose a workspace folder" default location (POSIX file (item 1 of argv))
return POSIX path of selectedFolder
on error number -128
return ""
end try
end run`
		return directoryPickerCommand{
			name: "osascript",
			args: []string{"-e", script, "--", initialPath},
		}, nil
	case "windows":
		const script = `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = 'Choose a workspace folder'; $dialog.SelectedPath = $args[0]; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $dialog.SelectedPath }`
		return directoryPickerCommand{
			name: "powershell.exe",
			args: []string{
				"-NoProfile", "-NonInteractive", "-Command", script, initialPath,
			},
		}, nil
	case "linux":
		if _, err := exec.LookPath("zenity"); err == nil {
			return directoryPickerCommand{
				name: "zenity",
				args: []string{
					"--file-selection", "--directory",
					"--title=Choose a workspace folder",
					"--filename=" + initialPath + string(filepath.Separator),
				},
			}, nil
		}
		return directoryPickerCommand{
			name: "kdialog",
			args: []string{
				"--getexistingdirectory", initialPath,
				"--title", "Choose a workspace folder",
			},
		}, nil
	default:
		return directoryPickerCommand{}, errors.New(
			"native directory selection is unsupported on this platform",
		)
	}
}

func directoryPickerCancelled(goos string, err error, output string) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	switch goos {
	case "linux":
		return exitErr.ExitCode() == 1 && output == ""
	case "windows":
		return exitErr.ExitCode() == 0 && output == ""
	default:
		return false
	}
}

package web

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
)

func TestNativeDirectoryPickerCommands(t *testing.T) {
	const initial = "/workspace/current"
	tests := []struct {
		name string
		goos string
		want directoryPickerCommand
	}{
		{
			name: "macOS uses AppleScript folder chooser",
			goos: "darwin",
			want: directoryPickerCommand{
				name: "osascript",
				args: []string{
					"-e",
					`on run argv
try
set selectedFolder to choose folder with prompt "Choose a workspace folder" default location (POSIX file (item 1 of argv))
return POSIX path of selectedFolder
on error number -128
return ""
end try
end run`,
					"--",
					initial,
				},
			},
		},
		{
			name: "Windows uses FolderBrowserDialog",
			goos: "windows",
			want: directoryPickerCommand{
				name: "powershell.exe",
				args: []string{
					"-NoProfile",
					"-NonInteractive",
					"-Command",
					`Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = 'Choose a workspace folder'; $dialog.SelectedPath = $args[0]; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $dialog.SelectedPath }`,
					initial,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nativeDirectoryPickerCommand(test.goos, initial)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("command = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNativeDirectoryPickerCommandRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := nativeDirectoryPickerCommand("unsupported", "/workspace"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}

func TestDirectoryPickerCancellation(t *testing.T) {
	err := exec.CommandContext(t.Context(), "sh", "-c", "exit 1").Run()
	if err == nil {
		t.Fatal("fixture command unexpectedly succeeded")
	}
	if !directoryPickerCancelled("linux", err, "") {
		t.Fatal("Linux picker cancellation was not recognized")
	}
	if directoryPickerCancelled("linux", context.Canceled, "") {
		t.Fatal("context cancellation was treated as picker cancellation")
	}
}

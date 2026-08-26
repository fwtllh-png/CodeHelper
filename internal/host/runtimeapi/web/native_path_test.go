package web

import (
	"reflect"
	"testing"
)

func TestNativeTextPathCommands(t *testing.T) {
	const target = "/workspace/file with spaces.go"
	tests := []struct {
		name string
		goos string
		want [][]string
	}{
		{
			name: "macOS prefers VS Code and retains native fallback",
			goos: "darwin",
			want: [][]string{
				{"open", "-a", "Visual Studio Code", target},
				{"open", "-t", target},
			},
		},
		{
			name: "Windows uses the file protocol handler",
			goos: "windows",
			want: [][]string{{
				"rundll32.exe",
				"url.dll,FileProtocolHandler",
				target,
			}},
		},
		{
			name: "Linux uses xdg-open",
			goos: "linux",
			want: [][]string{{"xdg-open", target}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nativeTextPathCommands(test.goos, target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("commands = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNativeTextPathCommandsRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := nativeTextPathCommands("unsupported", "/workspace/main.go"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}

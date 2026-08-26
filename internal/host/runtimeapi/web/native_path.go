package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func nativeTextPathOpener() func(context.Context, string) error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("open"); err == nil {
			return openNativeTextPath
		}
	case "windows":
		if _, err := exec.LookPath("rundll32.exe"); err == nil {
			return openNativeTextPath
		}
	case "linux":
		if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("xdg-open"); err == nil {
				return openNativeTextPath
			}
		}
	}
	return nil
}

func openNativeTextPath(ctx context.Context, target string) error {
	commands, err := nativeTextPathCommands(runtime.GOOS, target)
	if err != nil {
		return err
	}
	var failures []string
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
		output, err := command.CombinedOutput()
		if err == nil {
			return nil
		}
		failures = append(failures, fmt.Sprintf(
			"%s: %v: %s",
			arguments[0],
			err,
			strings.TrimSpace(string(output)),
		))
	}
	return fmt.Errorf("native path opener failed: %s", strings.Join(failures, "; "))
}

func nativeTextPathCommands(goos, target string) ([][]string, error) {
	switch goos {
	case "darwin":
		return [][]string{
			{"open", "-a", "Visual Studio Code", target},
			{"open", "-t", target},
		}, nil
	case "windows":
		return [][]string{{
			"rundll32.exe",
			"url.dll,FileProtocolHandler",
			target,
		}}, nil
	case "linux":
		return [][]string{{"xdg-open", target}}, nil
	default:
		return nil, errors.New(
			"native path opening is unsupported on this platform",
		)
	}
}

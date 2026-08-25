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
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", "-t", target)
	case "windows":
		command = exec.CommandContext(
			ctx,
			"rundll32.exe",
			"url.dll,FileProtocolHandler",
			target,
		)
	case "linux":
		command = exec.CommandContext(ctx, "xdg-open", target)
	default:
		return errors.New("native path opening is unsupported on this platform")
	}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"native path opener failed: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

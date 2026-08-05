package shell

import (
	"errors"
	"syscall"
)

func parseSignal(name string) (syscall.Signal, error) {
	switch name {
	case "INT":
		return syscall.SIGINT, nil
	case "TERM":
		return syscall.SIGTERM, nil
	case "KILL":
		return syscall.SIGKILL, nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "WINCH":
		return 0, errors.New("terminal resize signal is unavailable on windows")
	default:
		return 0, errors.New("unsupported terminal signal")
	}
}

//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	seccompDataNR     = 0
	seccompDataArch   = 4
	seccompDataArg0   = 16
	seccompErrnoEPERM = unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
)

func applyLinuxSyscallPolicy(mode string) error {
	if !validSyscallPolicy(mode) {
		return errors.New("invalid Linux syscall policy")
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set PR_SET_NO_NEW_PRIVS: %w", err)
	}
	filters, err := buildSeccompFilter(mode)
	if err != nil {
		return err
	}
	program := unix.SockFprog{
		Len: uint16(len(filters)), Filter: &filters[0],
	}
	if err := unix.Prctl(
		unix.PR_SET_SECCOMP,
		unix.SECCOMP_MODE_FILTER,
		uintptr(unsafe.Pointer(&program)),
		0,
		0,
	); err != nil {
		return fmt.Errorf("install seccomp filter: %w", err)
	}
	runtime.KeepAlive(filters)
	runtime.KeepAlive(program)
	value, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		return fmt.Errorf("verify PR_SET_NO_NEW_PRIVS: %w", err)
	}
	if value != 1 {
		return fmt.Errorf("verify PR_SET_NO_NEW_PRIVS: value=%d", value)
	}
	return nil
}

func buildSeccompFilter(mode string) ([]unix.SockFilter, error) {
	arch, err := seccompAuditArchitecture()
	if err != nil {
		return nil, err
	}
	filters := []unix.SockFilter{
		loadAbsolute(seccompDataArch),
		jumpEqual(arch, 1, 0),
		returnAction(unix.SECCOMP_RET_KILL_PROCESS),
		loadAbsolute(seccompDataNR),
	}
	for _, number := range []uint32{
		unix.SYS_PTRACE,
		unix.SYS_PROCESS_VM_READV,
		unix.SYS_PROCESS_VM_WRITEV,
		unix.SYS_IO_URING_SETUP,
		unix.SYS_IO_URING_ENTER,
		unix.SYS_IO_URING_REGISTER,
		unix.SYS_CLONE3,
		unix.SYS_UNSHARE,
		unix.SYS_SETNS,
	} {
		filters = append(filters, jumpEqual(number, 0, 1), returnAction(seccompErrnoEPERM))
	}
	filters = appendDangerousCloneFilter(filters)
	switch mode {
	case syscallPolicyRestricted:
		for _, number := range []uint32{
			unix.SYS_CONNECT, unix.SYS_ACCEPT, unix.SYS_ACCEPT4,
			unix.SYS_BIND, unix.SYS_LISTEN, unix.SYS_GETPEERNAME,
			unix.SYS_GETSOCKNAME, unix.SYS_SHUTDOWN, unix.SYS_SENDTO,
			unix.SYS_SENDMMSG, unix.SYS_RECVMMSG, unix.SYS_GETSOCKOPT,
			unix.SYS_SETSOCKOPT,
		} {
			filters = append(
				filters,
				jumpEqual(number, 0, 1),
				returnAction(seccompErrnoEPERM),
			)
		}
		filters = appendDomainFilter(filters, unix.SYS_SOCKET, unix.AF_UNIX)
		filters = appendDomainFilter(filters, unix.SYS_SOCKETPAIR, unix.AF_UNIX)
	case syscallPolicyProxyRouted:
		filters = appendIPDomainFilter(filters, unix.SYS_SOCKET)
		filters = appendDomainFilter(filters, unix.SYS_SOCKETPAIR, unix.AF_UNIX)
	case syscallPolicyDirect:
	}
	return append(filters, returnAction(unix.SECCOMP_RET_ALLOW)), nil
}

func appendDangerousCloneFilter(filters []unix.SockFilter) []unix.SockFilter {
	const dangerous = uint32(
		unix.CLONE_NEWCGROUP | unix.CLONE_NEWIPC | unix.CLONE_NEWNET |
			unix.CLONE_NEWNS | unix.CLONE_NEWPID | unix.CLONE_NEWUSER |
			unix.CLONE_NEWUTS | unix.CLONE_UNTRACED,
	)
	return append(filters,
		jumpEqual(unix.SYS_CLONE, 0, 3),
		loadAbsolute(seccompDataArg0),
		jumpSet(dangerous, 0, 1),
		returnAction(seccompErrnoEPERM),
		loadAbsolute(seccompDataNR),
	)
}

func appendDomainFilter(
	filters []unix.SockFilter,
	syscallNumber uint32,
	allowedDomain uint32,
) []unix.SockFilter {
	return append(filters,
		jumpEqual(syscallNumber, 0, 3),
		loadAbsolute(seccompDataArg0),
		jumpEqual(allowedDomain, 1, 0),
		returnAction(seccompErrnoEPERM),
		loadAbsolute(seccompDataNR),
	)
}

func appendIPDomainFilter(filters []unix.SockFilter, syscallNumber uint32) []unix.SockFilter {
	return append(filters,
		jumpEqual(syscallNumber, 0, 4),
		loadAbsolute(seccompDataArg0),
		jumpEqual(unix.AF_INET, 2, 0),
		jumpEqual(unix.AF_INET6, 1, 0),
		returnAction(seccompErrnoEPERM),
		loadAbsolute(seccompDataNR),
	)
}

func seccompAuditArchitecture() (uint32, error) {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	default:
		return 0, fmt.Errorf("seccomp is unsupported on %s", runtime.GOARCH)
	}
}

func loadAbsolute(offset uint32) unix.SockFilter {
	return unix.SockFilter{
		Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: offset,
	}
}

func jumpEqual(value uint32, yes, no uint8) unix.SockFilter {
	return unix.SockFilter{
		Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K,
		Jt:   yes, Jf: no, K: value,
	}
}

func jumpSet(value uint32, yes, no uint8) unix.SockFilter {
	return unix.SockFilter{
		Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K,
		Jt:   yes, Jf: no, K: value,
	}
}

func returnAction(action uint32) unix.SockFilter {
	return unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: action}
}

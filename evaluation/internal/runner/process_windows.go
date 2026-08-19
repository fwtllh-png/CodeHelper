//go:build windows

package runner

import (
	"errors"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureProcessTree(_ *exec.Cmd) {}

func attachProcessTree(command *exec.Cmd) (func() error, func() error, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, nil, err
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, nil, err
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(process)
		_ = windows.CloseHandle(job)
		return nil, nil, err
	}
	_ = windows.CloseHandle(process)

	var once sync.Once
	var closeErr error
	closeJob := func() error {
		once.Do(func() { closeErr = windows.CloseHandle(job) })
		return closeErr
	}
	kill := func() error {
		err := windows.TerminateJobObject(job, 1)
		return errors.Join(err, closeJob())
	}
	return kill, closeJob, nil
}

//go:build windows

package skill

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type stateFileLock struct {
	file *os.File
}

func acquireStateFileLock(path string) (*stateFileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0,
		1, 0, &overlapped,
	)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &stateFileLock{file: file}, nil
}

func (l *stateFileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	err := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &overlapped)
	return errors.Join(err, l.file.Close())
}

func rejectMultiplyLinkedState(path string, _ os.FileInfo) error {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path), windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return errors.New("skill enable state is multiply linked")
	}
	return nil
}

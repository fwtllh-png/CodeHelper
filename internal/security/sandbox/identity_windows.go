//go:build windows

package sandbox

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/windows"
)

type fileIdentity struct {
	device uint64
	inode  uint64
	links  uint64
}

func identityOf(path string, _ fs.FileInfo) (fileIdentity, error) {
	if path == "" {
		return fileIdentity{}, errors.New("Windows filesystem identity requires a path")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileIdentity{}, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fileIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return fileIdentity{}, err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fileIdentity{}, errors.New("filesystem object is a reparse point")
	}
	return fileIdentity{
		device: uint64(information.VolumeSerialNumber),
		inode: uint64(information.FileIndexHigh)<<32 |
			uint64(information.FileIndexLow),
		links: uint64(information.NumberOfLinks),
	}, nil
}

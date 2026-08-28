package artifactbroker

import (
	"os"

	"golang.org/x/sys/windows"
)

func linkedFile(_ string, file *os.File, _ os.FileInfo) bool {
	info, ok := windowsFileInfo(file)
	return !ok || info.NumberOfLinks != 1
}

func sameDevice(
	_, rootPath string,
	file *os.File,
	_, _ os.FileInfo,
) bool {
	sourceInfo, sourceOK := windowsFileInfo(file)
	root, err := os.Open(rootPath)
	if err != nil {
		return false
	}
	defer root.Close()
	rootInfo, rootOK := windowsFileInfo(root)
	return sourceOK && rootOK &&
		sourceInfo.VolumeSerialNumber == rootInfo.VolumeSerialNumber
}

func windowsFileInfo(file *os.File) (windows.ByHandleFileInformation, bool) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()),
		&info,
	)
	return info, err == nil
}

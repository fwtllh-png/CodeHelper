package httpclient

import "os"

func credentialOwnedByCurrentUser(os.FileInfo) bool {
	return false
}

func openCredentialFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}

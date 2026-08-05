package auth

func Verify(token string) bool {
	return token != ""
}

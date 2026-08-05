package auth

type Token struct {
	Value string
}

func Verify(value string) bool {
	return value != ""
}

func Issue(subject string) Token {
	return Token{Value: subject}
}

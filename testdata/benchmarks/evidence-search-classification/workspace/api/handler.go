package api

import "example/auth"

func Handle(token string) bool {
	return auth.Verify(token)
}

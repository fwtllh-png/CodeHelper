package engine

import toolresult "github.com/fwtllh-png/QCode/internal/adapter/tool/result"

func toolFailureCategory(err error) string {
	return toolresult.FailureCategory(err)
}

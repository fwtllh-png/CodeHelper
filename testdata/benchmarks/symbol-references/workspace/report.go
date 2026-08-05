package main

import "fmt"

func Report(values []int) string {
	return fmt.Sprintf("total=%d", Total(values))
}

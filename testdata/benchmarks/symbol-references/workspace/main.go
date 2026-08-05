package main

import "fmt"

// Subtotal is a near miss: a lexical scan must not report it as a use of Total.
func Subtotal(values []int) int {
	return Total(values[:1])
}

func main() {
	fmt.Println(Report([]int{1, 2}), Subtotal([]int{3}))
}

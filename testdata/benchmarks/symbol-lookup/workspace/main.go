package main

import "example/pkg"

func main() {
	store := pkg.NewStore()
	store.Put("greeting", "hello")
}

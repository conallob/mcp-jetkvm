package main

import (
	"log"

	"github.com/conallob/mcp-jetkvm/internal/jetkvm"
)

func main() {
	if err := jetkvm.RunServer(); err != nil {
		log.Fatal(err)
	}
}

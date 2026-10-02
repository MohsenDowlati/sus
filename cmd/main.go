package main

import (
	"github.com/MohsenDowlati/shorts/cmd/server"
	"log"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

package main

import (
	"log/slog"
	"os"

	"github.com/MohsenDowlati/shorts/cmd/server"
)

func main() {
	if err := server.Run(); err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}
}

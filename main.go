package main

import (
	"log/slog"
	"os"

	"chineseCards/cmd/server"
)

func main() {
	if err := server.Run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

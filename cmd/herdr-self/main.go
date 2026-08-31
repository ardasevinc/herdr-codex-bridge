package main

import (
	"context"
	"os"

	"github.com/ardasevinc/herdr-codex-bridge/internal/app"
)

func main() {
	os.Exit(app.Main(context.Background(), os.Args[1:]))
}

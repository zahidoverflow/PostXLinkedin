package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/bot"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	// Convenience for VPS/local runs: load .env if present.
	// systemd EnvironmentFile still works and will override as needed.
	_ = godotenv.Overload()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown for VPS/systemd.
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigC
		cancel()
	}()

	if err := bot.Run(ctx, logger, 30*time.Second); err != nil {
		logger.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

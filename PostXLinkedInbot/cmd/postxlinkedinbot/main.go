package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zahidoverflow/PostXLinkedin/PostXLinkedInbot/internal/bot"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

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

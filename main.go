package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gastownhall/tmux-adapter/internal/adapter"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tmux-adapter [flags]\n\n")
		fmt.Fprintf(os.Stderr, "WebSocket service that exposes gastown agents as a programmatic API.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  tmux-adapter --gt-dir ~/gt --port 8080\n")
		fmt.Fprintf(os.Stderr, "  tmux-adapter --gt-dir ~/gt --auth-token SECRET\n")
		fmt.Fprintf(os.Stderr, "  tmux-adapter --gt-dir ~/gt --debug-serve-dir ./samples\n")
	}

	gtDir := flag.String("gt-dir", filepath.Join(os.Getenv("HOME"), "gt"), "gastown town directory")
	port := flag.Int("port", 8080, "WebSocket server port")
	authToken := flag.String("auth-token", "", "optional WebSocket auth token (Bearer token or ?token=...)")
	allowedOrigins := flag.String("allowed-origins", "localhost:*", "comma-separated origin patterns for WebSocket CORS")
	debugServeDir := flag.String("debug-serve-dir", "", "serve static files from this directory at / (development only)")
	flag.Parse()

	var origins []string
	for _, o := range strings.Split(*allowedOrigins, ",") {
		if s := strings.TrimSpace(o); s != "" {
			origins = append(origins, s)
		}
	}

	a := adapter.New(*gtDir, *port, *authToken, origins, *debugServeDir)
	if err := a.Start(); err != nil {
		log.Fatal(err)
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	a.Stop()
}

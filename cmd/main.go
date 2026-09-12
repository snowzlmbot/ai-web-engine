package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/snowzlmbot/ai-web-engine/internal/api"
	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

var version = "1.0.0"

func main() {
	host := flag.String("host", "127.0.0.1", "bind host (loopback by default)")
	port := flag.Int("port", 6666, "listen port")
	skillsDir := flag.String("skills-dir", "skills", "skills root")
	configPath := flag.String("config", filepath.Join("config", "model_config.json"), "model config path")
	sessionsDir := flag.String("sessions-dir", "sessions", "encrypted sessions directory")
	logDir := flag.String("log-dir", "logs", "log directory")
	flag.Parse()
	if *host != "127.0.0.1" {
		log.Fatalf("refusing non-loopback host %q", *host)
	}
	if *port < 1024 || *port > 65535 {
		log.Fatalf("port must be between 1024 and 65535")
	}

	if err := os.MkdirAll(*logDir, 0o700); err != nil {
		log.Fatal(err)
	}
	logPath := filepath.Join(*logDir, "engine.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags|log.LUTC)
	logger.Printf("starting ai-web-engine version=%s host=%s port=%d", version, *host, *port)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Fatal(err)
	}
	cfg = config.ResolveAPIKey(cfg, os.Getenv)
	keyPath := filepath.Join(filepath.Dir(*configPath), "master.key")
	store, err := session.NewStore(*sessionsDir, keyPath)
	if err != nil {
		logger.Fatal(err)
	}
	server, err := api.New(*configPath, *skillsDir, cfg, store, logger)
	if err != nil {
		logger.Fatal(err)
	}
	addr := fmt.Sprintf("%s:%d", *host, *port)
	logger.Printf("ready addr=http://%s skills=%s", addr, *skillsDir)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		logger.Fatal(err)
	}
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/snowzlmbot/ai-web-engine/internal/api"
	"github.com/snowzlmbot/ai-web-engine/internal/buildinfo"
	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/integrity"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

var version = buildinfo.Version

func verifyScripts(args []string) error {
	flags := flag.NewFlagSet("verify-scripts", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	manifest := flags.String("manifest", filepath.Join("scripts", "manifest.json"), "signed integrity manifest")
	signature := flags.String("signature", filepath.Join("scripts", "manifest.sig"), "manifest signature")
	publicKey := flags.String("public-key", filepath.Join("scripts", "manifest.pub"), "Ed25519 public key")
	scriptsDir := flags.String("scripts-dir", "scripts", "scripts directory")
	expectedVersion := flags.String("version", buildinfo.Version, "expected release version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return integrity.Verify(*manifest, *signature, *publicKey, *scriptsDir, *expectedVersion)
}

func signScripts(args []string) error {
	flags := flag.NewFlagSet("sign-scripts", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	version := flags.String("version", buildinfo.Version, "release version")
	scriptsDir := flags.String("scripts-dir", "scripts", "scripts directory")
	privateKeyPath := flags.String("private-key", "", "Ed25519 PKCS#8 PEM private key")
	manifestPath := flags.String("manifest", filepath.Join("scripts", "manifest.json"), "output manifest")
	signaturePath := flags.String("signature", filepath.Join("scripts", "manifest.sig"), "output signature")
	publicKeyPath := flags.String("public-key", filepath.Join("scripts", "manifest.pub"), "output public key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*privateKeyPath) == "" {
		return errors.New("-private-key is required")
	}
	manifest, err := integrity.BuildManifest(*version, *scriptsDir)
	if err != nil {
		return err
	}
	privatePEM, err := os.ReadFile(*privateKeyPath)
	if err != nil {
		return err
	}
	signature, publicKey, err := integrity.SignManifest(manifest, privatePEM)
	if err != nil {
		return err
	}
	manifestBytes, err := integrity.MarshalManifest(manifest)
	if err != nil {
		return err
	}
	for path, data := range map[string][]byte{
		*manifestPath:  append(manifestBytes, '\n'),
		*signaturePath: integrity.EncodeSignature(signature),
		*publicKeyPath: integrity.EncodePublicKeyFile(publicKey),
	} {
		tmp := path + ".new"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify-scripts" {
		if err := verifyScripts(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "script integrity verification failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sign-scripts" {
		if err := signScripts(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "script signing failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	host := flag.String("host", "127.0.0.1", "bind host (loopback by default)")
	port := flag.Int("port", 6688, "listen port")
	skillsDir := flag.String("skills-dir", "skills", "required official skills root")
	localSkillsRoot := flag.String("local-skills-dir", filepath.Join(filepath.Dir(*skillsDir), "This machine skills"), "optional device-local skills root")
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

	cfg, err := config.LoadRuntime(*configPath, os.Getenv)
	if err != nil {
		logger.Fatal(err)
	}
	keyPath := filepath.Join(filepath.Dir(*configPath), "master.key")
	store, err := session.NewStore(*sessionsDir, keyPath)
	if err != nil {
		logger.Fatal(err)
	}
	server, err := api.NewWithLocalSkills(*configPath, *skillsDir, *localSkillsRoot, cfg, store, logger)
	if err != nil {
		logger.Fatal(err)
	}
	server.SetRestartFunc(func() {
		logger.Printf("restart requested through local Web UI")
		if err := syscall.Exec(os.Args[0], os.Args, os.Environ()); err != nil {
			logger.Printf("restart exec failed: %v", err)
		}
	})
	addr := fmt.Sprintf("%s:%d", *host, *port)
	logger.Printf("ready addr=http://%s skills=%s localSkills=%s localSkillsIndex=%s", addr, *skillsDir, *localSkillsRoot, filepath.Join(*localSkillsRoot, "skills-index.json"))
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		logger.Fatal(err)
	}
}

package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	store, err := OpenStore(envOr("DATA_FILE", "data/state.json"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	claude := NewClaudeFromEnv()
	srv := &Server{
		store:     store,
		claude:    claude,
		credsPath: envOr("CREDENTIALS_FILE", "data/credentials.json"),
		envKey:    os.Getenv("ANTHROPIC_API_KEY") != "",
	}

	// A key connected through the app earlier is picked up again after a restart.
	if !srv.envKey {
		key, err := loadCredentials(srv.credsPath)
		if err != nil {
			log.Fatalf("read %s: %v", srv.credsPath, err)
		}
		claude.SetKey(key)
	}

	addr := ":" + envOr("PORT", "8080")
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(envOr("STATIC_DIR", "../frontend/dist")),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	switch {
	case srv.envKey:
		log.Printf("Claude enabled from ANTHROPIC_API_KEY (model %s)", claude.Model)
	case claude.Enabled():
		log.Printf("Claude enabled from the key connected in the app (model %s)", claude.Model)
	default:
		log.Printf("No Anthropic API key yet: agent steps and chat are simulated until one is connected in the app")
	}
	log.Printf("listening on %s", addr)
	log.Fatal(httpSrv.ListenAndServe())
}

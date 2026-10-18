package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	store, err := OpenStore(envOr("DATA_FILE", "data/state.json"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	claude := NewClaudeFromEnv()
	srv := &Server{store: store, claude: claude}

	addr := ":" + envOr("PORT", "8080")
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(envOr("STATIC_DIR", "../frontend/dist")),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	if claude.Enabled() {
		log.Printf("Claude enabled (model %s)", claude.Model)
	} else {
		log.Printf("ANTHROPIC_API_KEY is not set: agent steps and chat will return simulated output")
	}
	log.Printf("listening on %s", addr)
	log.Fatal(httpSrv.ListenAndServe())
}

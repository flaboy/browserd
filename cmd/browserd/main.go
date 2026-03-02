package main

import (
	"fmt"
	"log"
	"net/http"

	"browserd/internal/config"
	"browserd/internal/router"
)

func main() {
	cfg := config.Load()
	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router.New(cfg),
	}
	log.Printf("browserd listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

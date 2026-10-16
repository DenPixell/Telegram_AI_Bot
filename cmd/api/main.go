// Точка входа HTTP API + веб-панели главы семьи.
package main

import (
	"log"

	"Telegram_AI_Bot/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("api starting on %s", cfg.HTTP.Addr)
	// TODO: storage -> services -> httpapi.NewServer(...).ListenAndServe()
}

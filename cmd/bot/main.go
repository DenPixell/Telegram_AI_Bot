// Точка входа Telegram-бота: принимает сообщения, фильтрует, отправляет в LLM, сохраняет траты.
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
	log.Printf("bot starting, llm model=%s", cfg.LLM.Model)
	// TODO: storage -> services -> bot.New(...).Run(ctx)
}

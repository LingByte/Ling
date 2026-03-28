package main

import (
	"log"

	"github.com/LingByte/Ling/internal/config"
)

func main() {
	err := config.Load()
	if err != nil {
		log.Println("Failed to load config: ", err)
		return
	}
	
	log.Println("Config loaded successfully")
	log.Println("Mode: ", config.GlobalConfig.Mode)
	log.Println("Base URL: ", config.GlobalConfig.BaseURL)
	log.Println("API Key: ", config.GlobalConfig.APIKey)
	log.Println("Model: ", config.GlobalConfig.Model)
}

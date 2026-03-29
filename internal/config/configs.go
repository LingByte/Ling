package config

import (
	"log"

	"github.com/LingByte/Ling/pkg/utils"
)

var GlobalConfig *Config

type Config struct {
	Mode    string `env:"MODE"`
	BaseURL string `env:"BASE_URL"`
	APIKey  string `env:"API_KEY"`
	Model   string `env:"MODEL"`
}

func Load() error {
	// 1. Load .env file based on environment (don't error if it doesn't exist, use default values)
	env := utils.GetEnv("MODE")
	err := utils.LoadEnv(env)
	if err != nil {
		// Only log when .env file doesn't exist, don't affect startup
		log.Printf("Note: .env file not found or failed to load: %v (using default values)", err)
	}
	GlobalConfig = &Config{
		Mode:    env,
		BaseURL: utils.GetEnv("BASE_URL"),
		APIKey:  utils.GetEnv("API_KEY"),
		Model:   utils.GetEnv("MODEL"),
	}
	return nil
}

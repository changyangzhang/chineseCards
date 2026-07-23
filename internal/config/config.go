package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBPath       string
	HTTPAddr     string
	OpenAIAPIKey string
	OpenAIModel  string
	NewPerDay    int
	ReverseCards bool
	BasicUser    string // HTTP basic auth username (empty = auth disabled)
	BasicPass    string // HTTP basic auth password
}

func Load() Config {
	return Config{
		DBPath:       envOr("DB_PATH", "chinese.db"),
		HTTPAddr:     envOr("HTTP_ADDR", ":8080"),
		OpenAIAPIKey: os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:  envOr("OPENAI_MODEL", "gpt-5-mini"),
		NewPerDay:    envInt("NEW_PER_DAY", 10),
		ReverseCards: envBool("REVERSE_CARDS", false),
		BasicUser:    os.Getenv("BASIC_USER"),
		BasicPass:    os.Getenv("BASIC_PASS"),
	}
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

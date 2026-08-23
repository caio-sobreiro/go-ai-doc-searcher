package main

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds the application configuration
type Config struct {
	DBHost           string
	DBPort           string
	DBUser           string
	DBPassword       string
	DBName           string
	OllamaHost       string
	EmbeddingModel   string
	ChatModel        string
	LLMBackend       string
	OpenRouterAPIKey string
	OpenRouterModel  string
	VectorDimensions string
	APIPort          string
	DocsDir          string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	// Load environment variables from .env file if it exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	return &Config{
		DBHost:           getEnv("DB_HOST", "postgres"),
		DBPort:           getEnv("DB_PORT", "5432"),
		DBUser:           getEnv("DB_USER", "postgres"),
		DBPassword:       getEnv("DB_PASSWORD", "postgres"),
		DBName:           getEnv("DB_NAME", "vectordb"),
		OllamaHost:       getEnv("OLLAMA_HOST", "http://ollama:11434"),
		EmbeddingModel:   getEnv("EMBEDDING_MODEL", "all-minilm"),
		ChatModel:        getEnv("CHAT_MODEL", "llama3.2:3b"),
		LLMBackend:       strings.ToLower(getEnv("LLM_BACKEND", "ollama")),
		OpenRouterAPIKey: getEnv("OPENROUTER_API_KEY", ""),
		OpenRouterModel:  getEnv("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
		VectorDimensions: getEnv("VECTOR_DIMENSIONS", "384"), // all-minilm uses 384 dimensions
		APIPort:          getEnv("API_PORT", "8080"),
		DocsDir:          getEnv("DOCS_DIR", "/app/documentation"),
	}
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

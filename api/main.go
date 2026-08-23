package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// Load configuration
	cfg := LoadConfig()

	// Connect to PostgreSQL
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatal("Failed to create connection pool:", err)
	}
	defer pool.Close()

	// Wait for database to be ready
	log.Println("Waiting for database to be ready...")
	if err := WaitForDB(ctx, pool, 30*time.Second); err != nil {
		log.Fatal("Database not ready:", err)
	}
	log.Println("Database is ready!")

	// Wait for Ollama to be ready
	log.Println("Waiting for Ollama to be ready...")
	if err := WaitForOllama(cfg.OllamaHost, 60*time.Second); err != nil {
		log.Fatal("Ollama not ready:", err)
	}
	log.Println("Ollama is ready!")

	// Initialize repository
	repo := NewRepository(pool)

	// Initialize database schema
	if err := repo.InitDB(ctx, cfg.VectorDimensions); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// Register pgvector types
	if err := repo.RegisterVectorTypes(ctx); err != nil {
		log.Fatal("Failed to register pgvector types:", err)
	}

	// Initialize service
	service := NewService(
		repo,
		cfg.OllamaHost,
		cfg.EmbeddingModel,
		cfg.ChatModel,
		cfg.LLMBackend,
		cfg.OpenRouterAPIKey,
		cfg.OpenRouterModel,
	)

	// Index documentation on startup (in background)
	var indexingDone sync.WaitGroup
	indexingDone.Add(1)
	go func() {
		defer indexingDone.Done()
		if _, err := os.Stat(cfg.DocsDir); os.IsNotExist(err) {
			log.Printf("Documentation directory not found, using sample data")
			service.IndexSampleData(ctx)
		} else {
			service.IndexDocumentation(ctx, cfg.DocsDir)
		}
		log.Println("\n✅ Indexing completed! API ready for search requests.")
	}()

	// Start HTTP API server
	apiServer := NewAPIServer(service, repo)

	http.HandleFunc("/search", apiServer.HandleSearch)
	http.HandleFunc("/health", apiServer.HandleHealth)
	http.HandleFunc("/stats", apiServer.HandleStats)

	log.Printf("🚀 API server starting on port %s (indexing in background)...", cfg.APIPort)
	if err := http.ListenAndServe(":"+cfg.APIPort, nil); err != nil {
		log.Fatal("Server failed:", err)
	}
}

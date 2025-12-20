package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// Repository handles all database operations
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new Repository instance
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// InitDB initializes the database (creates extension and tables)
func (r *Repository) InitDB(ctx context.Context, vectorDimensions string) error {
	// Enable pgvector extension
	if _, err := r.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("failed to create vector extension: %w", err)
	}

	// Create documents table if it doesn't exist
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS documents (
			id SERIAL PRIMARY KEY,
			file_path TEXT NOT NULL,
			chunk_index INT NOT NULL DEFAULT 0,
			content TEXT NOT NULL,
			embedding vector(%s) NOT NULL
		)
	`, vectorDimensions)
	if _, err := r.pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to create documents table: %w", err)
	}

	// Create index for faster similarity search
	indexQuery := `
		CREATE INDEX IF NOT EXISTS documents_embedding_idx
		ON documents USING ivfflat (embedding vector_cosine_ops)
		WITH (lists = 100)
	`
	if _, err := r.pool.Exec(ctx, indexQuery); err != nil {
		// Ignore error if there are not enough rows for the index
		log.Println("Note: Index will be created after more documents are added")
	}

	return nil
}

// RegisterVectorTypes registers pgvector types for the connection pool
func (r *Repository) RegisterVectorTypes(ctx context.Context) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	if err := pgxvec.RegisterTypes(ctx, conn.Conn()); err != nil {
		return fmt.Errorf("failed to register pgvector types: %w", err)
	}

	return nil
}

// InsertDocument inserts a document chunk into the database
func (r *Repository) InsertDocument(ctx context.Context, filePath string, chunkIndex int, content string, embedding pgvector.Vector) error {
	query := "INSERT INTO documents (file_path, chunk_index, content, embedding) VALUES ($1, $2, $3, $4)"
	_, err := r.pool.Exec(ctx, query, filePath, chunkIndex, content, embedding)
	return err
}

// SimilaritySearch performs a vector similarity search
func (r *Repository) SimilaritySearch(ctx context.Context, queryEmbedding pgvector.Vector, limit int) ([]Document, error) {
	// Get best matching chunks, but return unique files
	query := `
		WITH ranked_chunks AS (
			SELECT
				id,
				file_path,
				chunk_index,
				content,
				embedding <=> $1 as distance,
				ROW_NUMBER() OVER (PARTITION BY file_path ORDER BY embedding <=> $1) as rn
			FROM documents
		)
		SELECT id, file_path, chunk_index, content
		FROM ranked_chunks
		WHERE rn = 1
		ORDER BY distance
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, queryEmbedding, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.FilePath, &doc.ChunkIndex, &doc.Content); err != nil {
			return nil, err
		}
		results = append(results, doc)
	}

	return results, rows.Err()
}

// GetStats returns database statistics
func (r *Repository) GetStats(ctx context.Context) (int, int, error) {
	var totalChunks, totalFiles int
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*), COUNT(DISTINCT file_path) FROM documents").Scan(&totalChunks, &totalFiles)
	return totalChunks, totalFiles, err
}

// Ping checks database connectivity
func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// WaitForDB waits for the database to be ready
func WaitForDB(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for database")
		case <-ticker.C:
			if err := pool.Ping(ctx); err == nil {
				return nil
			}
		}
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// ftsWordRe matches Unicode letter sequences of 4+ characters for FTS term extraction.
// This intentionally excludes short stopword-like tokens (um, no, de, em…).
var ftsWordRe = regexp.MustCompile(`\pL{4,}`)

// techTermRe matches tokens containing underscores (e.g. UNIT_ID, DSERVER_DB_HOST, docker_compose).
// These are exact technical identifiers that benefit from literal ILIKE search, which is more
// specific than full-text (FTS normalises UNIT_ID → ['unit','id'] losing the compound structure).
var techTermRe = regexp.MustCompile(`\b\w+_\w[\w_]*\b`)

// extractTechTerm returns the first underscore-containing identifier found in text.
// Used to add an exact keyword lane in HybridSearch for queries like "UNIT_ID", "DSERVER_HOST".
func extractTechTerm(text string) string {
	return techTermRe.FindString(text)
}

// buildFTSTerms extracts significant words from free-form text and joins them with
// OR (|) for to_tsquery. OR semantics are critical for Q&A RAG: the user's question
// ("Como adicionar um novo UNIT_ID no Orthanc") shouldn't require ALL words to appear
// in the answer document — any meaningful keyword match is a positive signal.
func buildFTSTerms(text string) string {
	words := ftsWordRe.FindAllString(text, -1)
	seen := map[string]bool{}
	var terms []string
	for _, w := range words {
		lw := strings.ToLower(w)
		if seen[lw] {
			continue
		}
		seen[lw] = true
		// Sanitize single-quotes to avoid breaking to_tsquery syntax
		w = strings.ReplaceAll(w, "'", "")
		if w != "" {
			terms = append(terms, w)
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " | ")
}

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
			embedding vector(%s) NOT NULL,
			UNIQUE(file_path, chunk_index)
		)
	`, vectorDimensions)
	if _, err := r.pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to create documents table: %w", err)
	}

	// Create vector index for faster similarity search
	indexQuery := `
		CREATE INDEX IF NOT EXISTS documents_embedding_idx
		ON documents USING ivfflat (embedding vector_cosine_ops)
		WITH (lists = 100)
	`
	if _, err := r.pool.Exec(ctx, indexQuery); err != nil {
		// Ignore error if there are not enough rows for the index
		log.Println("Note: Vector index will be created after more documents are added")
	}

	// Drop any stale tsv generated column (may exist with wrong language config from earlier runs).
	// We use an expression index instead, which avoids storage/query config mismatch bugs.
	r.pool.Exec(ctx, "ALTER TABLE documents DROP COLUMN IF EXISTS tsv") //nolint:errcheck

	// Expression-based GIN index for full-text search using Portuguese config.
	// Portuguese config: strips stopwords (como, um, para, no…) and applies stemming
	// so "unidade" matches "unidad", "configurar" matches "configuração", etc.
	// The expression must match the WHERE clause in queries to hit this index.
	ftsIndexQuery := `
		CREATE INDEX IF NOT EXISTS documents_fts_idx
		ON documents USING GIN(to_tsvector('portuguese', content))
	`
	if _, err := r.pool.Exec(ctx, ftsIndexQuery); err != nil {
		log.Printf("Note: Could not create FTS index: %v", err)
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

// InsertDocument inserts a document chunk into the database (skips if already exists)
func (r *Repository) InsertDocument(ctx context.Context, filePath string, chunkIndex int, content string, embedding pgvector.Vector) error {
	query := `
		INSERT INTO documents (file_path, chunk_index, content, embedding)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (file_path, chunk_index) DO NOTHING
	`
	_, err := r.pool.Exec(ctx, query, filePath, chunkIndex, content, embedding)
	return err
}

// IsFileIndexed checks if a file has already been indexed
func (r *Repository) IsFileIndexed(ctx context.Context, filePath string) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM documents WHERE file_path = $1", filePath).Scan(&count)
	return count > 0, err
}

// SimilaritySearch performs a vector similarity search, returning the top unique-file matches
// Filters out results with cosine distance above maxDistance (0.0=identical, 2.0=opposite; typically filter >0.5 for poor matches)
func (r *Repository) SimilaritySearch(ctx context.Context, queryEmbedding pgvector.Vector, limit int, maxDistance float64) ([]Document, error) {
	// Get the single best-matching chunk per file, ordered by similarity
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
		SELECT id, file_path, chunk_index, content, distance
		FROM ranked_chunks
		WHERE rn = 1 AND distance <= $3
		ORDER BY distance
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, queryEmbedding, limit, maxDistance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.FilePath, &doc.ChunkIndex, &doc.Content, &doc.Distance); err != nil {
			return nil, err
		}
		results = append(results, doc)
	}

	return results, rows.Err()
}

// HybridSearch combines vector similarity and full-text search using Reciprocal Rank Fusion (RRF).
// Vector search handles semantic queries; FTS handles exact technical term matching (DICOM, Orthanc, UNIT_ID, etc.).
// Returns the best-scoring chunk per file, up to limit files, ordered by combined RRF score.
func (r *Repository) HybridSearch(ctx context.Context, queryEmbedding pgvector.Vector, queryText string, limit int) ([]Document, error) {
	// Build a lenient OR-style FTS query from significant words in the query.
	// OR semantics avoid the over-constrained AND behavior of plainto_tsquery on
	// natural language questions (where "como", "um", "novo" would all need to appear).
	ftsTerms := buildFTSTerms(queryText)

	if ftsTerms == "" {
		// No significant terms; fall back to pure vector search
		return r.SimilaritySearch(ctx, queryEmbedding, limit, 2.0)
	}

	// Check for technical identifiers (e.g., UNIT_ID, DSERVER_HOST) in the raw user query.
	// FTS normalises "UNIT_ID" → ['unit','id'] losing the compound — ILIKE finds it literally.
	techTerm := extractTechTerm(queryText)

	// RRF constant k=20: rank 1 scores 0.048, rank 9 scores 0.034, rank 20 scores 0.025.
	// Smaller k gives stronger preference to top-ranked docs vs. standard k=60.
	var query string
	var queryArgs []any

	if techTerm != "" {
		// 3-lane hybrid: vector + FTS + exact keyword match.
		// The exact lane guarantees docs with the literal technical term (e.g. UNIT_ID) surface
		// even when semantic and FTS signals are diluted by many loosely related documents.
		query = `
			WITH
			  vec AS (
			    SELECT id,
			           ROW_NUMBER() OVER (ORDER BY embedding <=> $1) AS rk
			    FROM documents
			    ORDER BY embedding <=> $1
			    LIMIT 100
			  ),
			  fts AS (
			    SELECT id,
			           ROW_NUMBER() OVER (ORDER BY ts_rank_cd(to_tsvector('portuguese', content), to_tsquery('portuguese', $2)) DESC) AS rk
			    FROM documents
			    WHERE to_tsvector('portuguese', content) @@ to_tsquery('portuguese', $2)
			    LIMIT 100
			  ),
			  kw AS (
			    SELECT id, 1 AS rk
			    FROM documents
			    WHERE content ILIKE '%' || $4 || '%'
			    LIMIT 50
			  ),
			  rrf AS (
			    SELECT
			      COALESCE(v.id, f.id, k.id) AS id,
			      COALESCE(1.0 / (v.rk::float + 20), 0.0)
			        + COALESCE(1.0 / (f.rk::float + 20), 0.0)
			        + COALESCE(1.0 / (k.rk::float + 20), 0.0) AS score
			    FROM vec v
			    FULL OUTER JOIN fts f ON f.id = v.id
			    FULL OUTER JOIN kw k ON k.id = COALESCE(v.id, f.id)
			  ),
			  best_per_file AS (
			    SELECT
			      d.id, d.file_path, d.chunk_index, d.content,
			      d.embedding <=> $1 AS distance,
			      r.score,
			      ROW_NUMBER() OVER (PARTITION BY d.file_path ORDER BY r.score DESC) AS rn
			    FROM rrf r
			    JOIN documents d ON d.id = r.id
			  )
			SELECT id, file_path, chunk_index, content, distance
			FROM best_per_file
			WHERE rn = 1
			ORDER BY score DESC
			LIMIT $3
		`
		queryArgs = []any{queryEmbedding, ftsTerms, limit, techTerm}
		log.Printf("HybridSearch: tech term exact match lane active for %q", techTerm)
	} else {
		// 2-lane hybrid: vector + FTS.
		query = `
			WITH
			  vec AS (
			    SELECT id,
			           ROW_NUMBER() OVER (ORDER BY embedding <=> $1) AS rk
			    FROM documents
			    ORDER BY embedding <=> $1
			    LIMIT 100
			  ),
			  fts AS (
			    SELECT id,
			           ROW_NUMBER() OVER (ORDER BY ts_rank_cd(to_tsvector('portuguese', content), to_tsquery('portuguese', $2)) DESC) AS rk
			    FROM documents
			    WHERE to_tsvector('portuguese', content) @@ to_tsquery('portuguese', $2)
			    LIMIT 100
			  ),
			  rrf AS (
			    SELECT
			      COALESCE(v.id, f.id) AS id,
			      COALESCE(1.0 / (v.rk::float + 20), 0.0) + COALESCE(1.0 / (f.rk::float + 20), 0.0) AS score
			    FROM vec v FULL OUTER JOIN fts f ON v.id = f.id
			  ),
			  best_per_file AS (
			    SELECT
			      d.id, d.file_path, d.chunk_index, d.content,
			      d.embedding <=> $1 AS distance,
			      r.score,
			      ROW_NUMBER() OVER (PARTITION BY d.file_path ORDER BY r.score DESC) AS rn
			    FROM rrf r
			    JOIN documents d ON d.id = r.id
			  )
			SELECT id, file_path, chunk_index, content, distance
			FROM best_per_file
			WHERE rn = 1
			ORDER BY score DESC
			LIMIT $3
		`
		queryArgs = []any{queryEmbedding, ftsTerms, limit}
	}

	rows, err := r.pool.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.FilePath, &doc.ChunkIndex, &doc.Content, &doc.Distance); err != nil {
			return nil, err
		}
		results = append(results, doc)
	}

	return results, rows.Err()
}

// GetChunkWindow retrieves a contiguous window of chunks centered on centerIdx.
// This provides coherent context to the LLM (e.g., steps before and after the matched step).
func (r *Repository) GetChunkWindow(ctx context.Context, filePath string, centerIdx int, windowSize int) ([]Document, error) {
	half := windowSize / 2
	minIdx := centerIdx - half
	if minIdx < 0 {
		minIdx = 0
	}

	query := `
		SELECT id, file_path, chunk_index, content
		FROM documents
		WHERE file_path = $1 AND chunk_index >= $2
		ORDER BY chunk_index
		LIMIT $3
	`

	rows, err := r.pool.Query(ctx, query, filePath, minIdx, windowSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.FilePath, &doc.ChunkIndex, &doc.Content); err != nil {
			return nil, err
		}
		chunks = append(chunks, doc)
	}

	return chunks, rows.Err()
}

// GetTopChunksForFile retrieves the N most semantically relevant chunks for a file
func (r *Repository) GetTopChunksForFile(ctx context.Context, filePath string, queryEmbedding pgvector.Vector, topN int) ([]Document, error) {
	query := `
		SELECT id, file_path, chunk_index, content, embedding <=> $2 as distance
		FROM documents
		WHERE file_path = $1
		ORDER BY distance
		LIMIT $3
	`

	rows, err := r.pool.Query(ctx, query, filePath, queryEmbedding, topN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.FilePath, &doc.ChunkIndex, &doc.Content, &doc.Distance); err != nil {
			return nil, err
		}
		chunks = append(chunks, doc)
	}

	return chunks, rows.Err()
}

// GetAllChunksForFile retrieves all chunks for a specific file, ordered by chunk_index
func (r *Repository) GetAllChunksForFile(ctx context.Context, filePath string) ([]Document, error) {
	query := `
		SELECT id, file_path, chunk_index, content
		FROM documents
		WHERE file_path = $1
		ORDER BY chunk_index
	`

	rows, err := r.pool.Query(ctx, query, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.FilePath, &doc.ChunkIndex, &doc.Content); err != nil {
			return nil, err
		}
		chunks = append(chunks, doc)
	}

	return chunks, rows.Err()
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

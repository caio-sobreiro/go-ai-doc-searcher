package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
)

// Service handles business logic
type Service struct {
	repo           *Repository
	ollamaHost     string
	embeddingModel string
	chatModel      string
}

// NewService creates a new Service instance
func NewService(repo *Repository, ollamaHost, embeddingModel, chatModel string) *Service {
	return &Service{
		repo:           repo,
		ollamaHost:     ollamaHost,
		embeddingModel: embeddingModel,
		chatModel:      chatModel,
	}
}

// GetEmbedding retrieves an embedding vector from Ollama
func (s *Service) GetEmbedding(text string) (pgvector.Vector, error) {
	reqBody := OllamaEmbeddingRequest{
		Model:  s.embeddingModel,
		Prompt: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return pgvector.Vector{}, err
	}

	resp, err := http.Post(
		s.ollamaHost+"/api/embeddings",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return pgvector.Vector{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return pgvector.Vector{}, fmt.Errorf("ollama API error: %s", string(body))
	}

	var embedResp OllamaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return pgvector.Vector{}, err
	}

	if len(embedResp.Embedding) == 0 {
		return pgvector.Vector{}, fmt.Errorf("no embeddings returned")
	}

	return pgvector.NewVector(embedResp.Embedding), nil
}

// GenerateAnswer generates an answer using the LLM based on search results
func (s *Service) GenerateAnswer(query string, results []Document) (string, error) {
	// Build context from search results (using FULL content)
	var contextParts []string
	for i, result := range results {
		contextParts = append(contextParts, fmt.Sprintf("Document %d (%s):\n%s", i+1, result.FilePath, result.Content))
	}
	context := strings.Join(contextParts, "\n\n---\n\n")

	// Build prompt
	systemPrompt := "You are a helpful assistant answering questions based on documentation. Use only the provided context to answer. If the context doesn't contain relevant information, say so."
	userPrompt := fmt.Sprintf("Context:\n%s\n\nQuestion: %s\n\nAnswer:", context, query)

	// Call Ollama chat API
	chatReq := OllamaChatRequest{
		Model: s.chatModel,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream: false,
	}

	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal chat request: %w", err)
	}

	resp, err := http.Post(s.ollamaHost+"/api/chat", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama chat API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama chat API returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp OllamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("failed to decode chat response: %w", err)
	}

	return chatResp.Message.Content, nil
}

// SummarizeWithLLM summarizes content using the LLM
func (s *Service) SummarizeWithLLM(content string) (string, error) {
	// If content is already short, don't summarize
	if len(content) < 600 {
		return content, nil
	}

	prompt := fmt.Sprintf(`Summarize this technical documentation in 3-5 concise sentences. Extract only the key facts, steps, and technical details. Remove all UI instructions, navigation elements, and formatting. Focus on WHAT and HOW, not WHERE to click.

Content:
%s

Summary (max 500 characters):`, content)

	chatReq := OllamaChatRequest{
		Model: s.chatModel,
		Messages: []ChatMessage{
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		// Fallback: truncate
		if len(content) > 800 {
			return content[:800], nil
		}
		return content, nil
	}

	resp, err := http.Post(s.ollamaHost+"/api/chat", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		// Fallback: truncate
		if len(content) > 800 {
			return content[:800], nil
		}
		return content, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Fallback: truncate
		if len(content) > 800 {
			return content[:800], nil
		}
		return content, nil
	}

	var chatResp OllamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		// Fallback: truncate
		if len(content) > 800 {
			return content[:800], nil
		}
		return content, nil
	}

	summary := strings.TrimSpace(chatResp.Message.Content)
	// If summary is too long or empty, truncate original
	if len(summary) == 0 || len(summary) > 850 {
		if len(content) > 800 {
			return content[:800], nil
		}
		return content, nil
	}
	return summary, nil
}

// IndexDocumentation indexes all markdown files in a directory
func (s *Service) IndexDocumentation(ctx context.Context, docsDir string) {
	log.Printf("Finding markdown files in %s...\n", docsDir)

	var mdFiles []string
	err := filepath.Walk(docsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".md") {
			mdFiles = append(mdFiles, path)
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Failed to walk documentation directory: %v", err)
	}

	log.Printf("Found %d markdown files. Starting indexing...\n", len(mdFiles))

	indexed := 0
	failed := 0
	skipped := 0

	for i, filePath := range mdFiles {
		if i%100 == 0 && i > 0 {
			log.Printf("Progress: %d/%d files (indexed: %d, failed: %d, skipped: %d)",
				i, len(mdFiles), indexed, failed, skipped)
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("Failed to read %s: %v", filePath, err)
			failed++
			continue
		}

		contentStr := string(content)

		// Skip empty files
		if len(strings.TrimSpace(contentStr)) == 0 {
			skipped++
			continue
		}

		// Get relative path for storage
		relPath, _ := filepath.Rel(docsDir, filePath)

		// Clean and chunk the content
		cleanedContent := CleanMarkdown(contentStr)
		chunks := ChunkByHeaders(cleanedContent, 2000) // Allow larger chunks, will summarize

		if len(chunks) == 0 {
			skipped++
			continue
		}

		// Index each chunk separately
		for chunkIdx, chunk := range chunks {
			chunk = strings.TrimSpace(chunk)
			if len(chunk) < 50 { // Skip tiny chunks
				skipped++
				continue
			}

			// Use LLM to summarize/clean the chunk if it's substantial
			if len(chunk) > 500 {
				summarized, err := s.SummarizeWithLLM(chunk)
				if err == nil && len(summarized) > 0 && len(summarized) < len(chunk) {
					chunk = summarized
				}
			}

			// Final safety check for embedding model limits
			if len(chunk) > 900 {
				chunk = chunk[:900]
			}

			embedding, err := s.GetEmbedding(chunk)
			if err != nil {
				if strings.Contains(err.Error(), "context length") {
					log.Printf("Chunk %d too long for %s, skipping chunk", chunkIdx, relPath)
					skipped++
				} else {
					log.Printf("Failed to get embedding for %s chunk %d: %v", relPath, chunkIdx, err)
					failed++
				}
				continue
			}

			if err := s.repo.InsertDocument(ctx, relPath, chunkIdx, chunk, embedding); err != nil {
				log.Printf("Failed to insert %s chunk %d: %v", relPath, chunkIdx, err)
				failed++
				continue
			}

			indexed++
		}
	}

	log.Printf("\n✅ Indexing complete!")
	log.Printf("  Successfully indexed: %d chunks", indexed)
	log.Printf("  Failed: %d chunks", failed)
	log.Printf("  Skipped: %d chunks", skipped)
	log.Printf("  Total files: %d", len(mdFiles))
}

// IndexSampleData indexes sample documents for testing
func (s *Service) IndexSampleData(ctx context.Context) {
	documents := []string{
		"Go is a statically typed, compiled programming language designed at Google.",
		"Python is a high-level, interpreted programming language known for its simplicity.",
		"JavaScript is a dynamic programming language commonly used for web development.",
		"Rust is a systems programming language focused on safety and performance.",
		"PostgreSQL is a powerful, open-source relational database system.",
		"Docker is a platform for developing, shipping, and running applications in containers.",
	}

	log.Printf("Generating embeddings using %s model and inserting sample documents...\n", s.embeddingModel)
	for i, content := range documents {
		embedding, err := s.GetEmbedding(content)
		if err != nil {
			log.Printf("Failed to get embedding: %v", err)
			continue
		}

		if err := s.repo.InsertDocument(ctx, "sample", i, content, embedding); err != nil {
			log.Printf("Failed to insert document: %v", err)
			continue
		}
		log.Printf("✓ Inserted: %s", content)
	}
}

// WaitForOllama waits for Ollama to be ready
func WaitForOllama(host string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Ollama")
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", host+"/api/tags", nil)
			if err != nil {
				continue
			}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

// CleanMarkdown removes metadata and noise from markdown content
func CleanMarkdown(content string) string {
	// Remove common Confluence/wiki metadata and noise
	lines := strings.Split(content, "\n")
	var cleaned []string
	skippingBreadcrumbs := true

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip YAML frontmatter
		if strings.HasPrefix(trimmed, "---") ||
			strings.HasPrefix(trimmed, "layout:") ||
			strings.HasPrefix(trimmed, "title:") ||
			strings.HasPrefix(trimmed, "parent:") ||
			strings.HasPrefix(trimmed, "nav_order:") {
			continue
		}

		// Skip navigation/UI elements
		if strings.Contains(trimmed, "[Edit this page]") ||
			strings.Contains(trimmed, "Table of contents") ||
			strings.HasPrefix(trimmed, "Created by") ||
			strings.HasPrefix(trimmed, "last modified") {
			continue
		}

		// Skip breadcrumb navigation (numbered lists with links at start of file)
		if skippingBreadcrumbs {
			// Breadcrumbs are numbered lists with markdown links
			if len(trimmed) > 0 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed, "[") && strings.Contains(trimmed, "](") {
				continue
			}
			// Once we hit a real header, stop skipping breadcrumbs
			if strings.HasPrefix(trimmed, "#") {
				skippingBreadcrumbs = false
			}
		}

		cleaned = append(cleaned, line)
	}

	// Remove image references: ![alt](path) and ![](path)
	result := strings.Join(cleaned, "\n")
	result = removeImageReferences(result)

	return strings.TrimSpace(result)
}

// removeImageReferences removes markdown image syntax
func removeImageReferences(content string) string {
	// Remove markdown images: ![alt](path)
	for {
		start := strings.Index(content, "![")
		if start == -1 {
			break
		}
		// Find the closing )
		end := strings.Index(content[start:], ")")
		if end == -1 {
			break
		}
		content = content[:start] + content[start+end+1:]
	}
	return content
}

// ChunkByHeaders splits content into chunks based on headers
func ChunkByHeaders(content string, maxChunkSize int) []string {
	var chunks []string
	lines := strings.Split(content, "\n")

	var currentChunk []string
	var currentSize int

	for _, line := range lines {
		// Start new chunk on headers (but not H1, keep doc together more)
		isHeader := strings.HasPrefix(strings.TrimSpace(line), "##")

		if isHeader && currentSize > 0 && currentSize+len(line) > maxChunkSize {
			// Save current chunk and start new one
			if len(currentChunk) > 0 {
				chunks = append(chunks, strings.Join(currentChunk, "\n"))
			}
			currentChunk = []string{line}
			currentSize = len(line)
		} else {
			currentChunk = append(currentChunk, line)
			currentSize += len(line) + 1 // +1 for newline

			// Also chunk if we exceed max size mid-section
			if currentSize > maxChunkSize {
				chunks = append(chunks, strings.Join(currentChunk, "\n"))
				currentChunk = []string{}
				currentSize = 0
			}
		}
	}

	// Don't forget the last chunk
	if len(currentChunk) > 0 {
		chunks = append(chunks, strings.Join(currentChunk, "\n"))
	}

	return chunks
}

// Truncate truncates a string to a maximum length
func Truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

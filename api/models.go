package main

import "github.com/pgvector/pgvector-go"

// Document represents a document chunk stored in the database
type Document struct {
	ID         int
	FilePath   string
	ChunkIndex int
	Content    string
	Embedding  pgvector.Vector
}

// OllamaEmbeddingRequest is the request payload for Ollama embeddings API
type OllamaEmbeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// OllamaEmbeddingResponse is the response from Ollama embeddings API
type OllamaEmbeddingResponse struct {
	Embedding []float32 `json:"embedding"`
}

// OllamaChatRequest is the request payload for Ollama chat API
type OllamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// ChatMessage represents a single message in a chat conversation
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OllamaChatResponse is the response from Ollama chat API
type OllamaChatResponse struct {
	Message ChatMessage `json:"message"`
}

// SearchRequest is the API request for document search
type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// SearchResponse is the API response containing search results and answer
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Query   string         `json:"query"`
	Answer  string         `json:"answer,omitempty"`
}

// SearchResult represents a single search result
type SearchResult struct {
	FilePath   string `json:"file_path"`
	ChunkIndex int    `json:"chunk_index"`
	Content    string `json:"content"`
}

// StatsResponse contains database statistics
type StatsResponse struct {
	TotalChunks int `json:"total_chunks"`
	TotalFiles  int `json:"total_files"`
}

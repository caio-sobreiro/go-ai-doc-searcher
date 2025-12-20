package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
)

// APIServer handles HTTP requests
type APIServer struct {
	service *Service
	repo    *Repository
}

// NewAPIServer creates a new APIServer instance
func NewAPIServer(service *Service, repo *Repository) *APIServer {
	return &APIServer{
		service: service,
		repo:    repo,
	}
}

// HandleSearch handles the search endpoint
func (s *APIServer) HandleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		http.Error(w, "Query is required", http.StatusBadRequest)
		return
	}

	if req.Limit == 0 {
		req.Limit = 3
	}

	ctx := r.Context()

	// Get embedding for the query
	queryEmbedding, err := s.service.GetEmbedding(req.Query)
	if err != nil {
		log.Printf("Failed to get query embedding: %v", err)
		http.Error(w, "Failed to process query", http.StatusInternalServerError)
		return
	}

	// Perform similarity search - gets best chunk per file
	topFiles, err := s.repo.SimilaritySearch(ctx, queryEmbedding, req.Limit)
	if err != nil {
		log.Printf("Search failed: %v", err)
		http.Error(w, "Search failed", http.StatusInternalServerError)
		return
	}

	// For each top file, get all chunks to reconstruct the complete file
	var completeFiles []Document
	var searchResults []SearchResult

	for _, topFile := range topFiles {
		// Get all chunks for this file
		allChunks, err := s.repo.GetAllChunksForFile(ctx, topFile.FilePath)
		if err != nil {
			log.Printf("Failed to get chunks for %s: %v", topFile.FilePath, err)
			continue
		}

		// Reconstruct complete file by joining all chunks
		var fullContent string
		for _, chunk := range allChunks {
			fullContent += chunk.Content + "\n\n"
		}

		// Add complete file for LLM context
		completeFiles = append(completeFiles, Document{
			FilePath: topFile.FilePath,
			Content:  fullContent,
		})

		// Add to search results (with truncated content for display)
		searchResults = append(searchResults, SearchResult{
			FilePath:   topFile.FilePath,
			ChunkIndex: 0, // Showing full file, not a specific chunk
			Content:    Truncate(fullContent, 200),
		})
	}

	// Generate answer using LLM with COMPLETE files as context
	answer := ""
	if len(completeFiles) > 0 {
		var err error
		answer, err = s.service.GenerateAnswer(req.Query, completeFiles)
		if err != nil {
			log.Printf("Failed to generate answer: %v", err)
			// Continue without answer rather than failing completely
		}
	}

	response := SearchResponse{
		Results: searchResults,
		Query:   req.Query,
		Answer:  answer,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleHealth handles the health check endpoint
func (s *APIServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	err := s.repo.Ping(ctx)

	status := map[string]string{
		"status": "healthy",
	}

	if err != nil {
		status["status"] = "unhealthy"
		status["error"] = err.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// HandleStats handles the statistics endpoint
func (s *APIServer) HandleStats(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	totalChunks, totalFiles, err := s.repo.GetStats(ctx)
	if err != nil {
		http.Error(w, "Failed to get stats", http.StatusInternalServerError)
		return
	}

	stats := StatsResponse{
		TotalChunks: totalChunks,
		TotalFiles:  totalFiles,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

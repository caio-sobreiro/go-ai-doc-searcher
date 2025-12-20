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

	// Perform similarity search
	results, err := s.repo.SimilaritySearch(ctx, queryEmbedding, req.Limit)
	if err != nil {
		log.Printf("Search failed: %v", err)
		http.Error(w, "Search failed", http.StatusInternalServerError)
		return
	}

	// Generate answer using LLM (with FULL content)
	answer := ""
	if len(results) > 0 {
		var err error
		answer, err = s.service.GenerateAnswer(req.Query, results)
		if err != nil {
			log.Printf("Failed to generate answer: %v", err)
			// Continue without answer rather than failing completely
		}
	}

	// Convert to API response (with truncated content for display)
	searchResults := make([]SearchResult, len(results))
	for i, doc := range results {
		searchResults[i] = SearchResult{
			FilePath:   doc.FilePath,
			ChunkIndex: doc.ChunkIndex,
			Content:    Truncate(doc.Content, 200),
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

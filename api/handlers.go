package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
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
		req.Limit = 5
	}

	ctx := r.Context()

	// HyDE: generate a hypothetical document fragment to improve the query embedding.
	// A plausible answer embeds closer to real documentation than a raw question does.
	// NOTE: HyDE is used ONLY for the vector embedding, NOT for FTS — LLM hallucinations
	// would introduce false vocabulary into FTS, biasing retrieval toward wrong documents.
	searchText := req.Query
	ftsText := req.Query
	if hydeAnswer, err := s.service.GenerateHypotheticalAnswer(ctx, req.Query); err == nil && hydeAnswer != "" {
		// Combine HyDE text with original query so both semantics are captured in the embedding
		searchText = hydeAnswer + "\n" + req.Query
		// FTS uses the raw user query — HyDE can hallucinate vocabulary not present in the corpus,
		// which would introduce false FTS signals. Raw query uses the user's actual terminology.
		ftsText = req.Query
		log.Printf("HyDE expansion (%d chars): %s...", len(hydeAnswer), hydeAnswer[:min(len(hydeAnswer), 120)])
	} else {
		log.Printf("HyDE unavailable, using raw query: %v", err)
	}

	// Embed the (HyDE-expanded) search text
	queryEmbedding, err := s.service.GetEmbedding(searchText)
	if err != nil {
		log.Printf("Failed to get query embedding: %v", err)
		http.Error(w, "Failed to process query", http.StatusInternalServerError)
		return
	}

	// Hybrid search: vector similarity + BM25 full-text, merged via Reciprocal Rank Fusion.
	// Vector finds semantically similar chunks; FTS finds exact technical terms (DICOM, Orthanc, etc.).
	topFiles, err := s.repo.HybridSearch(ctx, queryEmbedding, ftsText, req.Limit)
	if err != nil {
		log.Printf("Search failed: %v", err)
		http.Error(w, "Search failed", http.StatusInternalServerError)
		return
	}

	// For each matched file, retrieve a contiguous 3-chunk window centered on the best-matching
	// chunk. Contiguous windows give the LLM coherent context (e.g., steps before/after a match)
	// rather than scattered top-K chunks that may lack surrounding context.
	var llmDocs []Document
	var searchResults []SearchResult

	for _, topFile := range topFiles {
		window, err := s.repo.GetChunkWindow(ctx, topFile.FilePath, topFile.ChunkIndex, 3)
		if err != nil {
			log.Printf("Failed to get chunk window for %s: %v", topFile.FilePath, err)
			continue
		}

		// Combine window chunks into a single context block for the LLM
		var parts []string
		for _, c := range window {
			parts = append(parts, c.Content)
		}
		llmDocs = append(llmDocs, Document{
			FilePath: topFile.FilePath,
			Content:  strings.Join(parts, "\n\n"),
			Distance: topFile.Distance,
		})

		searchResults = append(searchResults, SearchResult{
			FilePath:   topFile.FilePath,
			ChunkIndex: topFile.ChunkIndex,
			Content:    Truncate(strings.Join(parts, " "), 200),
			Distance:   topFile.Distance,
		})
	}

	// Generate answer using LLM with the most relevant chunks as context
	answer := ""
	if len(llmDocs) > 0 {
		var err error
		answer, err = s.service.GenerateAnswer(req.Query, llmDocs)
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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pgvector/pgvector-go"
)

// Service handles business logic
type Service struct {
	repo           *Repository
	ollamaHost     string
	embeddingModel string
	chatModel      string
}

// SanitizeUTF8 removes invalid UTF-8 sequences from a string
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	// Convert to valid UTF-8, replacing invalid sequences with �
	v := make([]rune, 0, len(s))
	for i, r := range s {
		if r == utf8.RuneError {
			_, size := utf8.DecodeRuneInString(s[i:])
			if size == 1 {
				// Skip invalid byte
				continue
			}
		}
		v = append(v, r)
	}
	return string(v)
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

// OllamaEmbedRequest is the payload for the newer /api/embed endpoint (works for all models)
type OllamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// OllamaEmbedResponse is the response from /api/embed
type OllamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// GetEmbedding retrieves an embedding vector from Ollama using the /api/embed endpoint.
// This endpoint is supported by all modern Ollama models including nomic-embed-text.
func (s *Service) GetEmbedding(text string) (pgvector.Vector, error) {
	reqBody := OllamaEmbedRequest{
		Model: s.embeddingModel,
		Input: text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return pgvector.Vector{}, err
	}

	resp, err := http.Post(
		s.ollamaHost+"/api/embed",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return pgvector.Vector{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return pgvector.Vector{}, fmt.Errorf("ollama embed API error: %s", string(body))
	}

	var embedResp OllamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return pgvector.Vector{}, err
	}

	if len(embedResp.Embeddings) == 0 || len(embedResp.Embeddings[0]) == 0 {
		return pgvector.Vector{}, fmt.Errorf("no embeddings returned")
	}

	return pgvector.NewVector(embedResp.Embeddings[0]), nil
}

// confluenceIDSuffix matches trailing Confluence numeric IDs like "_1414496257"
var confluenceIDSuffix = regexp.MustCompile(`_\d{6,}$`)

// filePathToTitle converts a relative file path to a human-readable document title.
// It URL-decodes path components, strips Confluence numeric suffixes, and replaces hyphens with spaces.
// Example: "Confluence-Legacy/TSHOOTING/Falha-ao-enviar-todos-os-estudos_1930133656.md" → "Falha ao enviar todos os estudos"
func filePathToTitle(filePath string) string {
	base := filepath.Base(filePath)
	name := strings.TrimSuffix(base, filepath.Ext(base))

	// URL-decode (%2D → -, %7C → |, etc.)
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}

	// Strip Confluence export numeric suffix (e.g. "_1930133656")
	name = confluenceIDSuffix.ReplaceAllString(name, "")

	// Replace separators with spaces
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)

	return strings.TrimSpace(name)
}

// GenerateHypotheticalAnswer uses the LLM to produce a short hypothetical document fragment
// that would answer the query. This HyDE (Hypothetical Document Embeddings) technique
// generates a vector that is semantically closer to actual documentation than the raw query.
// Falls back gracefully: caller uses raw query if this returns an error.
func (s *Service) GenerateHypotheticalAnswer(ctx context.Context, query string) (string, error) {
	prompt := fmt.Sprintf(`Você é um especialista em sistemas PACS/RIS de imagem médica.
Gere 2-3 frases técnicas em português que um documento de documentação interna conteria para responder à pergunta abaixo.
Use terminologia técnica específica: nomes de sistemas (Orthanc, Mirth, PACS, ArgoCD, GitLab), caminhos de arquivo, comandos, nomes de configuração.
Seja direto — escreva como se fosse um trecho de documentação, não uma explicação.

Pergunta: %s

Trecho hipotético:`, query)

	chatReq := OllamaChatRequest{
		Model:    s.chatModel,
		Messages: []ChatMessage{{Role: "user", Content: prompt}},
		Stream:   false,
		Think:    false,
		Options:  map[string]any{"num_predict": 150},
	}

	reqBody, err := json.Marshal(chatReq)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ollamaHost+"/api/chat", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama API error %d: %s", resp.StatusCode, string(body))
	}

	var chatResp OllamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}

	return strings.TrimSpace(chatResp.Message.Content), nil
}

// GenerateAnswer generates an answer using the LLM based on retrieved chunks
func (s *Service) GenerateAnswer(query string, results []Document) (string, error) {
	// Build context from the most relevant chunks
	var contextParts []string
	for i, result := range results {
		contextParts = append(contextParts, fmt.Sprintf("[Fonte %d: %s]\n%s", i+1, result.FilePath, result.Content))
	}
	context := strings.Join(contextParts, "\n\n---\n\n")

	systemPrompt := `Você é um assistente que responde perguntas técnicas SOMENTE com base nos documentos fornecidos.

PROCESSO OBRIGATÓRIO:
1. Leia o contexto fornecido.
2. Verifique se os documentos respondem a pergunta de forma DIRETA e EXPLÍCITA.
3. Se SIM: responda usando apenas o que está textualmente nos documentos. Cite [Fonte N].
4. Se NÃO (contexto irrelevante ou insuficiente): escreva APENAS "Não encontrei essa informação na documentação disponível." — nada mais.

PROIBIDO: inventar procedimentos, usar conhecimento geral, inferir etapas não documentadas.`

	userPrompt := fmt.Sprintf("Documentos:\n%s\n\n---\nPergunta: %s\n\nResposta (baseada EXCLUSIVAMENTE nos documentos acima):", context, query)

	// Call Ollama chat API with think:false to suppress qwen3 reasoning tokens.
	// temperature:0 reduces creativity and hallucination — we want grounded answers.
	chatReq := OllamaChatRequest{
		Model: s.chatModel,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:  false,
		Think:   false,
		Options: map[string]any{"temperature": 0.0, "num_predict": 800},
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

// IndexDocumentation indexes all markdown files in a directory using parallel workers.
// No LLM summarization is performed — original text is embedded directly for better
// semantic fidelity and dramatically faster indexing.
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

	log.Printf("Found %d markdown files. Starting parallel indexing (4 workers)...\n", len(mdFiles))

	type result struct {
		indexed int
		failed  int
		skipped int
	}

	jobs := make(chan string, len(mdFiles))
	results := make(chan result, len(mdFiles))

	worker := func() {
		for filePath := range jobs {
			relPath, _ := filepath.Rel(docsDir, filePath)

			already, err := s.repo.IsFileIndexed(ctx, relPath)
			if err == nil && already {
				results <- result{skipped: 1}
				continue
			}

			content, err := os.ReadFile(filePath)
			if err != nil {
				log.Printf("Failed to read %s: %v", relPath, err)
				results <- result{failed: 1}
				continue
			}

			contentStr := SanitizeUTF8(string(content))
			if len(strings.TrimSpace(contentStr)) == 0 {
				results <- result{skipped: 1}
				continue
			}

			// nomic-embed-text supports up to 8192 tokens; use larger chunks
			// to preserve more context per embedding.
			cleanedContent := CleanMarkdown(contentStr)
			chunks := ChunkByHeaders(cleanedContent, 1500)

			if len(chunks) == 0 {
				results <- result{skipped: 1}
				continue
			}

			var indexed, failed, skipped int
			chunkIdx := 0
			title := filePathToTitle(relPath)
			for _, chunk := range chunks {
				chunk = strings.TrimSpace(SanitizeUTF8(chunk))
				if len(chunk) < 50 {
					skipped++
					continue
				}

				// Title-enriched embed text: the embedding captures both document topic
				// (from title) and local content. The stored content is just the chunk
				// so the LLM receives clean, unmodified text.
				embedText := title + "\n" + chunk
				embedding, err := s.GetEmbedding(embedText)
				if err != nil {
					log.Printf("⚠️  Embedding failed for %s chunk %d: %v", relPath, chunkIdx, err)
					failed++
					continue
				}

				if err := s.repo.InsertDocument(ctx, relPath, chunkIdx, chunk, embedding); err != nil {
					log.Printf("Failed to insert %s chunk %d: %v", relPath, chunkIdx, err)
					failed++
					continue
				}

				indexed++
				chunkIdx++
			}
			results <- result{indexed: indexed, failed: failed, skipped: skipped}
		}
	}

	// Start 4 parallel workers
	numWorkers := 4
	for range numWorkers {
		go worker()
	}

	for _, f := range mdFiles {
		jobs <- f
	}
	close(jobs)

	totalIndexed, totalFailed, totalSkipped := 0, 0, 0
	for range mdFiles {
		r := <-results
		totalIndexed += r.indexed
		totalFailed += r.failed
		totalSkipped += r.skipped
	}

	log.Printf("\n✅ Indexing complete!")
	log.Printf("  Successfully indexed: %d chunks", totalIndexed)
	log.Printf("  Failed: %d chunks", totalFailed)
	log.Printf("  Skipped: %d chunks", totalSkipped)
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
	result = stripHyperlinkURLs(result)

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
		end := strings.Index(content[start:], ")")
		if end == -1 {
			break
		}
		content = content[:start] + content[start+end+1:]
	}
	return content
}

// stripHyperlinkURLs converts [text](url) → text, removing noisy URLs from
// embedded/LLM context while preserving the human-readable anchor text.
func stripHyperlinkURLs(content string) string {
	var result strings.Builder
	i := 0
	for i < len(content) {
		if content[i] != '[' {
			result.WriteByte(content[i])
			i++
			continue
		}
		// Find the matching ]
		bracketEnd := strings.Index(content[i+1:], "]")
		if bracketEnd == -1 {
			result.WriteByte(content[i])
			i++
			continue
		}
		bracketEnd = i + 1 + bracketEnd // absolute index of ]
		afterBracket := bracketEnd + 1
		// Must be followed by (url)
		if afterBracket >= len(content) || content[afterBracket] != '(' {
			result.WriteByte(content[i])
			i++
			continue
		}
		parenEnd := strings.Index(content[afterBracket+1:], ")")
		if parenEnd == -1 {
			result.WriteByte(content[i])
			i++
			continue
		}
		parenEnd = afterBracket + 1 + parenEnd // absolute index of )
		// Write anchor text only, skip the URL
		anchorText := content[i+1 : bracketEnd]
		result.WriteString(anchorText)
		i = parenEnd + 1
	}
	return result.String()
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

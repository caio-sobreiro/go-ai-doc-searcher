# Documentation Search with RAG

A full-stack application for searching documentation using AI-powered RAG (Retrieval-Augmented Generation) with local LLM models.

## Features

- 🚀 **Local LLMs** - Uses Ollama for embeddings and chat (no API keys needed!)
- 🔍 **Semantic Search** - Vector similarity search using PostgreSQL and pgvector
- 🤖 **AI Answers** - Get contextual answers from your documentation using RAG
- 📄 **Document Processing** - Markdown cleaning, chunking, and LLM-based summarization
- 🎨 **Modern UI** - Clean SvelteKit interface with real-time search
- 🐳 **Fully Containerized** - Docker Compose setup for easy deployment
- 💻 **Runs Offline** - Everything local after initial model downloads

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   SvelteKit UI  │────▶│   Go API Server  │────▶│   PostgreSQL    │
│  (Port 5173)    │     │   (Port 8080)    │     │   + pgvector    │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                               │
                               ▼
                        ┌──────────────┐
                        │    Ollama    │
                        │  Embeddings  │
                        │     Chat     │
                        └──────────────┘
```

### Components

- **Frontend** (`frontend/`) - SvelteKit UI with Tailwind CSS
- **API** (`api/`) - Go backend with RAG implementation
- **Database** - PostgreSQL 16 with pgvector extension
- **LLM** - Ollama with all-minilm (embeddings) and llama3.2:3b (chat)

## Quick Start

1. **Install Ollama** ([ollama.ai](https://ollama.ai)) and pull models:
   ```bash
   ollama pull all-minilm
   ollama pull llama3.2:3b
   ```

2. **Start the backend**:
   ```bash
   cd api
   docker compose up --build -d
   ```

3. **Start the frontend**:
   ```bash
   cd frontend
   npm install
   npm run dev
   ```

4. **Open the UI**: [http://localhost:5173](http://localhost:5173)

## How It Works

### 1. Document Indexing

The system indexes markdown documentation with the following pipeline:

```
Markdown Files
  ↓
Clean (remove metadata, images, breadcrumbs)
  ↓
Chunk by headers (~2000 chars)
  ↓
Summarize with LLM (if >500 chars)
  ↓
Truncate to 900 chars (model limit)
  ↓
Generate embedding (384 dims)
  ↓
Store in PostgreSQL
```

### 2. Search & RAG

When a user searches:

```
User Query
  ↓
Generate embedding
  ↓
Vector similarity search (cosine distance)
  ↓
Retrieve top N documents (with full content)
  ↓
Build context prompt
  ↓
LLM generates answer
  ↓
Return answer + source documents
```

## Project Structure

```
.
├── api/                    # Go backend
│   ├── main.go            # Entry point
│   ├── models.go          # Type definitions
│   ├── config.go          # Configuration
│   ├── repository.go      # Database layer
│   ├── service.go         # Business logic & LLM integration
│   ├── handlers.go        # HTTP handlers
│   └── Dockerfile         # Backend container
│
├── frontend/              # SvelteKit UI
│   ├── src/
│   │   ├── lib/
│   │   │   ├── api.ts    # API client
│   │   │   └── types.ts  # TypeScript types
│   │   └── routes/
│   │       └── +page.svelte  # Main search page
│   └── vite.config.ts    # Vite config with proxy
│
├── documentation/         # Your markdown docs (to be indexed)
└── docker-compose.yml    # Container orchestration
```

## API Endpoints

### POST /search
Search documentation and get AI-generated answers

**Request:**
```json
{
  "query": "how to create a login?",
  "limit": 3
}
```

**Response:**
```json
{
  "query": "how to create a login?",
  "answer": "To create a login, follow these steps...",
  "results": [
    {
      "file_path": "docs/login.md",
      "chunk_index": 0,
      "content": "Login creation involves..."
    }
  ]
}
```

### GET /health
Health check

### GET /stats
Database statistics (total chunks and files indexed)

## Models Used

### Embeddings: all-minilm
- **Size**: ~23MB
- **Dimensions**: 384
- **Speed**: Very fast
- **Use**: Convert text to vector embeddings

### Chat: llama3.2:3b
- **Size**: ~2GB
- **Context**: 128K tokens
- **Use**: Generate answers from documentation context

## Development

### Backend (Go)

```bash
cd api

# Run with Docker
docker compose up --build

# Or run locally
export DB_HOST=localhost
export OLLAMA_HOST=http://localhost:11434
go run *.go
```

### Frontend (SvelteKit)

```bash
cd frontend

# Install dependencies
npm install

# Development server
npm run dev

# Production build
npm run build
npm run preview
```

## Configuration

### Backend (.env in api/)

```env
DB_HOST=postgres
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=vectordb
OLLAMA_HOST=http://ollama:11434
EMBEDDING_MODEL=all-minilm
CHAT_MODEL=llama3.2:3b
VECTOR_DIMENSIONS=384
API_PORT=8080
DOCS_DIR=/app/documentation
```

### Frontend (.env in frontend/)

```env
VITE_API_URL=/api  # Proxy to backend during dev
```

## Adding Your Documentation

1. Place markdown files in `documentation/` directory
2. Restart the API - it will automatically index on startup
3. Check indexing progress in logs:
   ```bash
   docker logs go-vector-app -f
   ```

## Tech Stack

### Backend
- **Go 1.23** - Backend language
- **pgx v5** - PostgreSQL driver with vector support
- **pgvector** - Vector similarity extension
- **Ollama** - Local LLM inference

### Frontend
- **SvelteKit 2** - Web framework
- **Svelte 5** - UI with runes
- **TypeScript** - Type safety
- **Tailwind CSS 4** - Styling
- **Vite 7** - Build tool

## Why Local LLMs?

- ✅ **No API costs** - completely free after initial download
- ✅ **Privacy** - your data never leaves your machine
- ✅ **Offline** - works without internet (after model download)
- ✅ **Fast** - optimized models process quickly
- ✅ **No rate limits** - unlimited queries

## Performance

- **Indexing**: ~1,300 markdown files → ~2,300 chunks in ~10-15 minutes
- **Search**: <1 second for embedding + retrieval
- **Answer Generation**: 2-5 seconds depending on context size
- **Memory**: ~2GB for llama3.2:3b model

## License

MIT


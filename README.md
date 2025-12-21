# Documentation Search with RAG

A full-stack RAG (Retrieval-Augmented Generation) application for searching Portuguese technical documentation using local LLMs. Features server-side rendering, intelligent chunking, and vector similarity search.

## Features

- 🚀 **Local LLMs** - Uses Ollama for embeddings and chat (no API keys needed!)
- 🔍 **Semantic Search** - Vector similarity search using PostgreSQL and pgvector
- 🤖 **AI Answers** - Get contextual answers in Portuguese from your documentation
- 📄 **Smart Processing** - LLM summarization before chunking to preserve context
- 🎨 **Modern SSR UI** - SvelteKit with server-side rendering
- 🐳 **Fully Containerized** - Docker Compose setup for easy deployment
- 💻 **Runs Offline** - Everything local after initial model downloads
- 🔄 **Resume-Safe Indexing** - Gracefully handles restarts during indexing

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   SvelteKit UI  │────▶│   Go API Server  │────▶│   PostgreSQL    │
│  SSR (Port 8080)│     │  (Internal only) │     │   + pgvector    │
│   proxies /api  │     │                  │     │                 │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                               │
                               ▼
                        ┌──────────────┐
                        │    Ollama    │
                        │  (Host OS)   │
                        │  Embeddings  │
                        │     Chat     │
                        └──────────────┘
```

### Components

- **Frontend** (`frontend/`) - SvelteKit UI with SSR, Tailwind CSS, and API proxy
- **API** (`api/`) - Go backend with RAG implementation and UTF-8 sanitization
- **Database** - PostgreSQL 16 with pgvector extension (384-dimensional vectors)
- **LLM** - Ollama with all-minilm (embeddings) and llama3.2:3b (chat)

## Quick Start

1. **Install Ollama** ([ollama.ai](https://ollama.ai)) and pull models:
   ```bash
   ollama pull all-minilm
   ollama pull llama3.2:3b
   ```

2. **Add your documentation**:
   ```bash
   # Place markdown files in documentation/ directory
   cp -r /path/to/your/docs documentation/
   ```

3. **Start everything**:
   ```bash
   docker compose up --build -d
   ```

4. **Access the UI**: [http://localhost:8080](http://localhost:8080)

The system will automatically index your documentation on startup. Check progress:
```bash
docker compose logs app -f
```

## How It Works

### 1. Document Indexing

The system indexes markdown documentation with an optimized pipeline:

```
Markdown Files
  ↓
UTF-8 Sanitization (remove invalid bytes)
  ↓
Clean (remove metadata, images, breadcrumbs)
  ↓
Chunk by headers (~2000 chars)
  ↓
LLM Summarize (condense to ~400-800 chars, preserves Portuguese)
  ↓
Split if still >512 chars (512 char chunks, 100 char overlap)
  ↓
Generate embedding (384 dims)
  ↓
Store in PostgreSQL (with UNIQUE constraint on file_path + chunk_index)
```

**Key Features:**
- Summarizes BEFORE splitting to preserve context
- Skips already-indexed files on restart (resume-safe)
- Handles UTF-8 encoding issues gracefully
- Portuguese-optimized prompts

### 2. Search & RAG

When a user searches:

```
User Query
  ↓
Generate embedding (384 dims)
  ↓
Vector similarity search (cosine distance)
  ↓
Find top 3 most relevant FILES (not just chunks)
  ↓
Retrieve ALL chunks for each file
  ↓
Reconstruct complete files
  ↓
Build context prompt in Portuguese
  ↓
LLM generates answer in Portuguese
  ↓
Return answer + source files
```

**Context Strategy:** Instead of feeding individual chunks, the system retrieves the top 3 most relevant complete files, providing better context for the LLM.

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
│   │       ├── +page.svelte       # Main search page
│   │       └── +page.server.ts    # SSR load function
│   ├── vite.config.ts    # Vite config with API proxy
│   └── Dockerfile        # Frontend container (dev mode)
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

### Full Stack (Recommended)

```bash
# Start all services (postgres, api, frontend)
docker compose up --build -d

# View logs
docker compose logs app -f      # API logs
docker compose logs frontend -f  # Frontend logs

# Stop all services
docker compose down
```

Access at: [http://localhost:8080](http://localhost:8080)

### Backend Only (Go)

```bash
cd api

# Run with Docker
docker compose up --build

# Or run locally
export DB_HOST=localhost
export OLLAMA_HOST=http://localhost:11434
go run *.go
```

### Frontend Only (SvelteKit)

```bash
cd frontend

# Install dependencies
npm install

# Development server (with HMR)
npm run dev

# Production build
npm run build
npm run preview
```

**Note:** Frontend proxies `/api` requests to backend at `http://localhost:8080` in dev mode.

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
# API proxy configuration (handled by Vite)
API_URL=http://app:8080  # Internal Docker network
ORIGIN=http://localhost:8080  # Public URL
```

**SSR Note:** The frontend uses SvelteKit's server-side rendering to fetch stats before rendering, eliminating the initial API call from the browser.

## Adding Your Documentation

1. Place markdown files in `documentation/` directory
2. Restart the API - it will automatically index on startup:
   ```bash
   docker compose restart app
   ```
3. Check indexing progress in logs:
   ```bash
   docker compose logs app -f
   ```

**Indexing Features:**
- Automatically skips already-indexed files on restart
- Handles UTF-8 encoding errors gracefully
- Shows progress every 100 files
- Displays summary statistics when complete

## Tech Stack

### Backend
- **Go 1.23** - Backend language
- **pgx v5** - PostgreSQL driver with vector support
- **pgvector** - Vector similarity extension
- **Ollama** - Local LLM inference

### Frontend
- **SvelteKit 2** - Web framework with SSR
- **Svelte 5** - UI with runes
- **TypeScript** - Type safety
- **Tailwind CSS 4** - Styling
- **Vite 7** - Build tool with API proxy

## Why Local LLMs?

- ✅ **No API costs** - completely free after initial download
- ✅ **Privacy** - your data never leaves your machine
- ✅ **Offline** - works without internet (after model download)
- ✅ **Fast** - optimized models process quickly
- ✅ **No rate limits** - unlimited queries

## Performance

- **Indexing**: ~1,300 markdown files → ~2,300 chunks in ~10-15 minutes
  - Summarization: ~3-5 seconds per chunk
  - Handles restarts gracefully (skips already-indexed files)
- **Search**: <1 second for embedding + vector similarity search
- **Answer Generation**: 2-5 seconds depending on context size
- **Memory**: ~2GB for llama3.2:3b model
- **Database**: UNIQUE constraint prevents duplicates, vector index for fast search

## License

MIT


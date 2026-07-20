# Archie Backend — Implementation Plan

## 1. Project Overview

**Archie** is an "Agentic AI for everyone" platform — a mobile-first app where users manage AI agent tasks, converse with AI models (Archie 1.6 Max/Pro/Lite), configure AI providers (OpenAI, Anthropic, Ollama), manage skill bundles (AgentSkills.io compliant), store knowledge entries, and control their agent's capabilities.

### Architecture

```
┌─────────────────────┐     REST/WS      ┌─────────────────────┐
│   Flutter Frontend  │ ◄──────────────►  │   Go Backend        │
│   (Riverpod + Dio)  │                   │   (stdlib net/http) │
└─────────────────────┘                   │                     │
                                           │   ┌─────────────┐   │
                                           │   │ Pocketbase  │   │
                                           │   │ (embedded)  │   │
                                           │   └─────────────┘   │
                                           │   ┌─────────────┐   │
                                           │   │ AI Provider  │   │
                                           │   │ Proxy (LLM) │   │
                                           │   └─────────────┘   │
                                           └─────────────────────┘
```

- **Pocketbase** provides the storage layer (embedded SQLite), auto-generates CRUD REST APIs, handles auth, and offers real-time subscriptions.
- **Go stdlib** (`net/http`, `gorilla/websocket`) serves:
  - Custom REST handlers that augment Pocketbase (business logic, validation, auth middleware)
  - WebSocket hub for real-time chat streaming
  - AI provider proxy (routes model requests to OpenAI/Anthropic/Ollama with unified API)
- **WebSocket** streams chat messages token-by-token for real-time AI responses.

---

## 2. Phased Implementation Plan

### Phase 0: Project Scaffolding & Infrastructure (Day 1)

**Goal**: Runnable Go server with Pocketbase embedded, health check, configuration, and clean project structure.

#### 2.1 Project Layout

```
/work/apps/archie-core/
├── cmd/
│   └── server/
│       └── main.go                  # Entrypoint: init Pocketbase, mount routes, start server
├── internal/
│   ├── config/
│   │   └── config.go                # Env-based config (PB data dir, port, JWT secret, provider API keys)
│   ├── server/
│   │   ├── server.go                # Server struct, HTTP mux setup, graceful shutdown
│   │   ├── middleware.go            # CORS, request logging, auth middleware, rate limiting
│   │   └── routes.go                # Route registration
│   ├── auth/
│   │   └── auth_handler.go          # Custom auth endpoints (login, register, verify, token refresh)
│   ├── tasks/
│   │   ├── task_model.go            # DB schema helpers / Pocketbase collection mapping
│   │   ├── task_handler.go          # REST handlers for tasks
│   │   └── task_service.go          # Business logic for tasks (scheduling, filtering)
│   ├── chat/
│   │   ├── chat_handler.go          # REST handlers for conversations / messages
│   │   ├── chat_service.go          # Business logic: create conversation, add messages
│   │   └── websocket.go             # WebSocket hub: connect/disconnect, streaming, broadcast
│   ├── skills/
│   │   ├── skill_handler.go         # REST handlers for skills
│   │   ├── skill_service.go         # Skill discovery, AgentSkills.io spec loading
│   │   └── skill_model.go           # Skill bundle model
│   ├── knowledge/
│   │   ├── knowledge_handler.go     # REST handlers for knowledge entries
│   │   └── knowledge_service.go     # CRUD + full-text search
│   ├── providers/
│   │   ├── provider_handler.go      # REST handlers for AI provider config
│   │   ├── provider_service.go      # Provider config management, key validation
│   │   └── proxy.go                 # AI provider proxy (OpenAI, Anthropic, Ollama)
│   ├── models/
│   │   └── model_handler.go         # REST handlers for available models listing
│   └── common/
│       ├── response.go              # Standard JSON response helpers
│       ├── errors.go                # Error types, error response formatting
│       └── validation.go            # Input validation helpers
├── migrations/
│   └── 001_initial_schema.go        # Pocketbase collection definitions
├── pb_data/                         # Pocketbase data directory (gitignored)
├── pb_public/                       # Admin UI assets (if using PB's built-in)
├── go.mod
├── go.sum
├── .env.example
├── Makefile
└── PLAN.md
```

#### 2.2 Dependencies (go.mod)

| Module | Purpose |
|---|---|
| `github.com/pocketbase/pocketbase` | Embedded Pocketbase server |
| `github.com/gorilla/websocket` | WebSocket implementation |
| `github.com/go-chi/chi/v5` (or stdlib `net/http` mux in Go 1.22+) | HTTP routing — since Go 1.22 supports method-based routing (`GET /tasks`, `POST /tasks`) with stdlib, we can avoid chi |
| `github.com/golang-jwt/jwt/v5` | JWT token generation/validation |
| `golang.org/x/crypto` | Password hashing (bcrypt) |
| `github.com/sashabaranov/go-openai` | OpenAI API client |
| `github.com/liushuangls/go-anthropic` | Anthropic API client |
| `github.com/joho/godotenv` | .env file loading |

#### 2.3 Pocketbase Schema — Collection Definitions

**Collections** (each created via Pocketbase migration or admin API):

| Collection            | Fields                                                                 | Auth?  | API Rules                |
|-----------------------|------------------------------------------------------------------------|--------|--------------------------|
| `users`               | Built-in Pocketbase users collection (email, password, name, avatar)   | Yes    | PB-managed auth          |
| `conversations`       | `user` (relation→users), `title` (text), `model` (text), `provider` (text), `created`, `updated` | No  | Owner-only CRUD         |
| `messages`            | `conversation` (relation→conversations), `user` (relation→users), `role` (select: user/assistant/system), `content` (text/json), `tokens_used` (number), `created` | No | Owner read, system write |
| `tasks`               | `user` (relation→users), `title` (text), `subtitle` (text), `type` (select: Agent/Scheduled/Favorites), `icon` (text), `scheduled_at` (date, optional), `status` (select: pending/in_progress/completed/failed), `created`, `updated` | No | Owner-only CRUD         |
| `skills`              | `user` (relation→users, optional — null = official), `name` (text, unique per user), `description` (text), `enabled` (bool), `official` (bool), `source_path` (text, optional), `metadata` (json, for SKILL.md frontmatter), `created`, `updated` | No | Owner read/write for user skills, read-only for official |
| `knowledge`           | `user` (relation→users), `title` (text), `content` (text), `enabled` (bool), `created`, `updated` | No | Owner-only CRUD          |
| `provider_configs`    | `user` (relation→users), `provider` (select: openai/anthropic/ollama), `api_key_encrypted` (text), `base_url` (text, optional), `is_active` (bool), `created`, `updated` | No | Owner-only CRUD          |
| `settings`            | `user` (relation→users, unique), `theme` (select: light/dark/system), `language` (text, default "en"), `selected_model` (text), `created`, `updated` | No | Owner-only CRUD          |

> **Note:** For MVP, we can skip encryption on `api_key_encrypted` and use reversible encryption with a server-side key. For V1, we'll use Pocketbase's built-in encryption or envelope encryption.

#### 2.4 Configuration (.env / env vars)

```env
PORT=8090
PB_DATA_DIR=./pb_data
JWT_SECRET=your-256-bit-secret
ENCRYPTION_KEY=32-byte-key-for-provider-api-keys

# Provider defaults (used if user hasn't configured their own)
OPENAI_API_KEY=
ANTHROPIC_API_KEY=
OLLAMA_BASE_URL=http://localhost:11434

CORS_ALLOWED_ORIGINS=http://localhost:3000,http://localhost:5173
```

---

### Phase 1: Core Server Foundation (Day 2-3)

**Goal**: Server boots, Pocketbase initializes with schema, health endpoint works, CORS + auth middleware operational.

#### 1.1 Server Bootstrap (`cmd/server/main.go`)

```go
func main() {
    cfg := config.Load()
    pbApp := pocketbase.NewWithConfig(pocketbase.Config{
        DefaultDataDir: cfg.PBDataDir,
    })

    srv := server.New(cfg, pbApp)
    srv.RegisterRoutes()

    if err := pbApp.Start(); err != nil {
        log.Fatal(err)
    }
}
```

#### 1.2 Auth Middleware (`internal/server/middleware.go`)

- **JWT verification middleware** — extracts `Authorization: Bearer <token>`, validates JWT, injects `user_id` into context
- **CORS middleware** — allows configurable origins, methods, headers
- **Request logging middleware** — method, path, status, duration
- **Rate limiting** — per-IP / per-user rate limiting (optional in MVP)

#### 1.3 Route Registration (`internal/server/routes.go`)

```
GET  /api/health                    → health check (no auth)

# Auth (Pocketbase built-in + custom endpoints)
POST /api/auth/register             → register new user (Pocketbase users)
POST /api/auth/login                → login, returns JWT
POST /api/auth/refresh              → refresh JWT
GET  /api/auth/me                   → current user profile

# Tasks (all require auth)
GET    /api/tasks                   → list tasks (filterable by type, status)
POST   /api/tasks                   → create task
GET    /api/tasks/:id               → get task details
PATCH  /api/tasks/:id               → update task
DELETE /api/tasks/:id               → delete task

# Conversations & Messages (all require auth)
GET    /api/conversations           → list user's conversations
POST   /api/conversations           → create conversation
GET    /api/conversations/:id       → get conversation with messages
DELETE /api/conversations/:id       → delete conversation
POST   /api/conversations/:id/messages  → send message (returns via WS via REST)
WS     /api/ws/chat/:conversation_id    → WebSocket: stream AI responses

# Skills (all require auth)
GET    /api/skills                  → list skills (official + user's)
POST   /api/skills                  → create custom skill
GET    /api/skills/:id              → get skill details
PATCH  /api/skills/:id              → update skill (toggle enabled, edit)
DELETE /api/skills/:id              → delete custom skill

# Knowledge (all require auth)
GET    /api/knowledge               → list knowledge entries
POST   /api/knowledge               → create entry
GET    /api/knowledge/:id           → get entry details
PATCH  /api/knowledge/:id           → update entry
DELETE /api/knowledge/:id           → delete entry

# Providers (all require auth)
GET    /api/providers               → list configured providers
POST   /api/providers               → add/update provider config
DELETE /api/providers/:provider     → remove provider config
POST   /api/providers/:provider/validate  → test API key connectivity

# Models (all require auth)
GET    /api/models                  → list available models (aggregated from all active providers)

# Settings (all require auth)
GET    /api/settings                → get user settings
PATCH  /api/settings                → update user settings

# Static files for Pocketbase admin UI
/*                                    → serve pb_public/
```

---

### Phase 2: Authentication & User Management (Day 3-4)

**Goal**: Full auth flow working — register, login, JWT, token refresh, middleware enforcement.

#### 2.1 Pocketbase Auth Integration

Pocketbase provides:
- Built-in `users` collection with email/password auth
- JWT token generation + verification
- Token refresh endpoints

We expose our own handlers (`/api/auth/register`, `/api/auth/login`, `/api/auth/refresh`) that:
1. Delegate to Pocketbase's auth API internally
2. Add custom response shaping (include user profile data)
3. Support additional fields (name, avatar)

#### 2.2 Custom Auth Handler (`internal/auth/auth_handler.go`)

- **POST /api/auth/register**: validates input, calls `pb.Users().Create()`, returns JWT + user
- **POST /api/auth/login**: calls `pb.Users().AuthWithPassword()`, returns JWT + user
- **POST /api/auth/refresh**: validates existing JWT, returns new JWT
- **GET /api/auth/me**: returns current user profile from `pb.Users().GetOne()`

#### 2.3 Middleware Integration

Auth middleware extracts `user_id` from validated JWT and sets it on context. All subsequent handlers read `user_id` from context for record ownership filtering.

---

### Phase 3: Task Management (Day 4-5)

**Goal**: Full CRUD for agent tasks, filtering by type (All/Agent/Scheduled/Favorites), icon mapping.

#### 3.1 Task Schema Mapping

Mapping frontend model to Pocketbase:
```json
{
  "id": "abc123",
  "user": "user_rel_id",
  "title": "Finishing Home Hub Website with ...",
  "subtitle": "I sincerely apologize for the previous mistake...",
  "type": "Agent",          // "Agent" | "Scheduled" | "Favorites"
  "icon": "auto_awesome_motion",  // string key, frontend maps to IconData
  "status": "in_progress",  // "pending" | "in_progress" | "completed" | "failed"
  "scheduled_at": null,     // ISO date or null
  "created": "2025-10-28T00:00:00Z",
  "updated": "2025-10-28T00:00:00Z"
}
```

#### 3.2 Task Handlers

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/tasks` | List user's tasks. Query params: `type` (Agent/Scheduled/Favorites), `status`, `search` (text search on title/subtitle) |
| POST | `/api/tasks` | Create task. Validates required fields. Auto-sets `user` from JWT. |
| GET | `/api/tasks/:id` | Get single task (ownership check) |
| PATCH | `/api/tasks/:id` | Update task fields (title, subtitle, type, status, icon) |
| DELETE | `/api/tasks/:id` | Soft-delete or hard-delete task |

#### 3.3 Filter Logic

The frontend's `FilterRow` sends the selected filter as a query param:
- `?type=Agent` — return tasks where type = "Agent"
- `?type=Scheduled` — return tasks where type = "Scheduled"
- `?type=Favorites` — return tasks where type = "Favorites"
- No filter or `?type=All` — return all tasks for user

---

### Phase 4: Chat System — REST + WebSocket (Day 5-8)

**Goal**: Full conversational AI with real-time token streaming via WebSocket, conversation history via REST.

#### 4.1 Data Model

**Conversation:**
```json
{
  "id": "conv_001",
  "user": "user_rel_id",
  "title": "Building a dashboard",
  "model": "Archie 1.6 Pro",
  "provider": "anthropic",
  "created": "2025-10-28T00:00:00Z",
  "updated": "2025-10-28T01:00:00Z"
}
```

**Message:**
```json
{
  "id": "msg_001",
  "conversation": "conv_001",
  "user": "user_rel_id",
  "role": "user",          // "user" | "assistant" | "system"
  "content": "Hello! Can you help me build a dashboard?",
  "tokens_used": 15,
  "created": "2025-10-28T00:00:00Z"
}
```

#### 4.2 REST Endpoints for Conversations

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/conversations` | List user's conversations, ordered by `updated` desc. Supports `?search=` to filter by title. |
| POST | `/api/conversations` | Create conversation. Body: `{ "title": "...", "model": "...", "provider": "..." }`. Auto-sets `user`. |
| GET | `/api/conversations/:id` | Get conversation metadata + all messages. Supports pagination (`?page=1&perPage=50`). |
| PATCH | `/api/conversations/:id` | Update conversation (rename title, change model) |
| DELETE | `/api/conversations/:id` | Delete conversation + all messages |
| POST | `/api/conversations/:id/messages` | Send a message. Body: `{ "content": "..." }`. Creates user message, triggers AI, creates assistant message. Returns the assistant message once complete (synchronous fallback). |

#### 4.3 WebSocket Hub (`internal/chat/websocket.go`)

**Architecture:**

```
Client (Flutter) ──WS──► Hub (per-server singleton)
                            │
                    ┌───────┴───────┐
                    │   Connection  │  (per-client, per-conversation)
                    │   Manager     │
                    └───────┬───────┘
                            │
              ┌─────────────┼─────────────┐
              │             │             │
        OpenAI Proxy  Anthropic Proxy  Ollama Proxy
```

**WebSocket Protocol:**

**Client → Server messages:**
```json
// Send a chat message
{
  "type": "message",
  "conversation_id": "conv_001",
  "content": "Build me a dashboard",
  "model": "Archie 1.6 Pro"
}

// Join a conversation room (subscribe to streaming)
{
  "type": "join",
  "conversation_id": "conv_001"
}

// Leave a conversation room
{
  "type": "leave",
  "conversation_id": "conv_001"
}

// Ping (keep-alive)
{
  "type": "ping"
}
```

**Server → Client messages:**
```json
// Streaming token from AI
{
  "type": "token",
  "conversation_id": "conv_001",
  "token": "build",
  "index": 0
}

// Stream complete
{
  "type": "done",
  "conversation_id": "conv_001",
  "message_id": "msg_002",
  "tokens_used": 412
}

// Error during streaming
{
  "type": "error",
  "conversation_id": "conv_001",
  "code": "provider_error",
  "message": "OpenAI API rate limit exceeded"
}

// System notification
{
  "type": "system",
  "conversation_id": "conv_001",
  "message": "Model switched to Archie 1.6 Lite"
}

// Pong (keep-alive response)
{
  "type": "pong"
}
```

**Hub Design:**

```go
type Hub struct {
    mu          sync.RWMutex
    connections map[string]*Client        // clientID → Client
    rooms       map[string]map[string]*Client // conversationID → { clientID → Client }
}

type Client struct {
    id     string
    userID string
    conn   *websocket.Conn
    send   chan []byte
    hub    *Hub
}

type ProxyService struct {
    openai    *openai.Client
    anthropic *anthropic.Client
    ollama    *ollama.Client
}
```

- Clients register on connect, unregister on disconnect
- `join` message adds client to a room (conversation)
- `message` type triggers AI provider call with streaming
- Hub broadcasts tokens to all clients in the conversation room
- Only the sender's client receives the token stream by default (single-user conversations in MVP)

#### 4.4 AI Provider Proxy (`internal/providers/proxy.go`)

**Unified interface:**

```go
type AIProvider interface {
    ChatStream(ctx context.Context, req ChatRequest) (<-chan Token, error)
    Chat(ctx context.Context, req ChatRequest) (string, int, error)
}

type ChatRequest struct {
    Model    string
    Messages []Message
    Config   ProviderConfig
}

type Token struct {
    Content string
    Index   int
    Done    bool
    Err     error
}
```

**Provider implementations:**

| Provider | Client Library | Model Mapping |
|----------|---------------|---------------|
| OpenAI | `go-openai` | `gpt-4o`, `gpt-4o-mini`, etc |
| Anthropic | `go-anthropic` | `claude-sonnet-4`, `claude-haiku-3.5`, etc |
| Ollama | Raw HTTP | Maps to local `OLLAMA_BASE_URL` + model name |

**Model Name Resolution:**
```
Frontend Model Name          →    Provider + Backend Model
──────────────────────────────────────────────────────
Archie 1.6 Max               →    anthropic / claude-sonnet-4-20250514
Archie 1.6 Pro               →    openai / gpt-4o
Archie 1.6 Lite              →    openai / gpt-4o-mini (or ollama/llama3)
```

The resolution is configurable via `settings` so the user can map Archie model tiers to their preferred providers.

#### 4.5 Message Flow (WebSocket Path)

```
1. Client sends WS message: { "type": "message", "conversation_id": "abc", "content": "Hello" }
2. Server validates JWT, verifies ownership
3. Server saves user message to Pocketbase (messages collection)
4. Server loads conversation history from Pocketbase (last N messages for context)
5. Server constructs system prompt (includes enabled skills + knowledge context)
6. Server calls AIProvider.ChatStream()
7. For each token received from provider → WS broadcast to room
8. On stream complete → save assistant message to Pocketbase, broadcast "done" with message_id + tokens_used
```

**Fallback (REST-only path without WS):**

The `POST /api/conversations/:id/messages` endpoint works synchronously, returning the full assistant response as JSON after the AI completes. This is used when the client cannot establish WebSocket (e.g., initial development, degraded mode).

---

### Phase 5: Skills Management (Day 8-10)

**Goal**: CRUD for skills, official library (read-only), custom skills, AgentSkills.io SKILL.md spec integration.

#### 5.1 Skill Model

```json
{
  "id": "skill_001",
  "user": null,              // null = official skill
  "name": "automation-and-scheduling",
  "description": "MUST read before requests involving automated execution...",
  "enabled": true,
  "official": true,
  "source_path": null,       // filesystem path for local skills
  "metadata": {
    "version": "1.0.0",
    "author": "Archie",
    "tags": ["automation", "scheduling"]
  },
  "created": "2025-05-08T00:00:00Z",
  "updated": "2025-05-08T00:00:00Z"
}
```

#### 5.2 REST Endpoints

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/skills` | List skills: official skills + user's custom skills. Query: `?enabled=true` to filter. |
| POST | `/api/skills` | Create custom skill. Body: `{ "name": "...", "description": "...", "metadata": {...} }` |
| GET | `/api/skills/:id` | Get skill details |
| PATCH | `/api/skills/:id` | Update skill (toggle enabled, edit name/description). Official skills: only `enabled` toggleable. |
| DELETE | `/api/skills/:id` | Delete custom skill only |

#### 5.3 AgentSkills.io Integration

- **Official skills** are seeded into Pocketbase via migration (JSON from AgentSkills.io registry)
- **Custom skills** can be created via REST API
- When a conversation starts, the server builds a system prompt that includes enabled skills' instructions (progressive disclosure: only skill names/descriptions in base context, full SKILL.md content loaded on demand)
- The `GET /api/skills/:id/instructions` endpoint returns the full SKILL.md content of a skill for the AI to reference

#### 5.4 Skill Loading in Chat Context

When a user starts a conversation:

1. Server loads all `enabled=true` skills for the user
2. Injects **catalog** (name + description) into system prompt (~50-100 tokens each)
3. When the AI decides a skill is relevant, it can request the full instructions via a tool call (future) or the server includes all enabled skill instructions in the prompt (MVP simplification)

---

### Phase 6: Knowledge Management (Day 10-11)

**Goal**: CRUD for knowledge entries, text search, injection into AI context.

#### 6.1 Knowledge Model

```json
{
  "id": "knowledge_001",
  "user": "user_rel_id",
  "title": "Design and feature preferences for AI-powered family dashboard",
  "content": "When building AI-powered family dashboard applications...",
  "enabled": true,
  "created": "2025-10-28T00:00:00Z",
  "updated": "2025-10-28T00:00:00Z"
}
```

#### 6.2 REST Endpoints

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/knowledge` | List knowledge entries. Query: `?search=` for text search on title+content, `?enabled=true` |
| POST | `/api/knowledge` | Create entry. Body: `{ "title": "...", "content": "..." }` |
| GET | `/api/knowledge/:id` | Get entry |
| PATCH | `/api/knowledge/:id` | Update entry |
| DELETE | `/api/knowledge/:id` | Delete entry |

#### 6.3 Knowledge Injection

Enabled knowledge entries are included in the AI's system prompt during chat (similar to skills but with full content for small entries, or summarized for large ones).

---

### Phase 7: Provider Configuration & Model Management (Day 11-12)

**Goal**: Users configure AI providers (API keys), see available models, switch models in chat.

#### 7.1 Provider Config Model

```json
{
  "id": "provider_001",
  "user": "user_rel_id",
  "provider": "openai",
  "api_key_encrypted": "enc:AES256:base64...",
  "base_url": null,  // optional custom URL (e.g., for reverse proxy)
  "is_active": true,
  "created": "2025-10-28T00:00:00Z",
  "updated": "2025-10-28T00:00:00Z"
}
```

#### 7.2 API Key Encryption

- Use AES-256-GCM with a server-side `ENCRYPTION_KEY`
- Keys are encrypted at rest, decrypted in memory when proxying AI requests
- Never log or expose API keys in responses (return `has_key: true` instead)

#### 7.3 REST Endpoints

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/providers` | List configured providers (sanitized: no key in response, just `has_key: bool`) |
| POST | `/api/providers` | Add/update provider config. Body: `{ "provider": "openai", "api_key": "sk-...", "base_url": null }`. Encrypts key before storing. |
| DELETE | `/api/providers/:provider` | Remove provider config |
| POST | `/api/providers/:provider/validate` | Test connectivity by making a small API call to the provider |

#### 7.4 Model Listing

`GET /api/models` aggregates available models from all active providers:

```json
{
  "models": [
    {
      "id": "gpt-4o",
      "name": "Archie 1.6 Pro",
      "provider": "openai",
      "tier": "pro",
      "capabilities": ["chat", "function_calling", "vision"],
      "available": true
    },
    {
      "id": "claude-sonnet-4-20250514",
      "name": "Archie 1.6 Max",
      "provider": "anthropic",
      "tier": "max",
      "capabilities": ["chat", "function_calling", "extended_thinking"],
      "available": true
    },
    {
      "id": "gpt-4o-mini",
      "name": "Archie 1.6 Lite",
      "provider": "openai",
      "tier": "lite",
      "available": true
    }
  ]
}
```

This endpoint powers the frontend's `ModelSelector` dropdown.

---

### Phase 8: User Settings (Day 12)

**Goal**: User preferences (theme, language, selected model) stored and fetched.

#### 8.1 Settings Model

```json
{
  "id": "settings_001",
  "user": "user_rel_id",
  "theme": "system",        // "light" | "dark" | "system"
  "language": "en",
  "selected_model": "Archie 1.6 Lite",
  "created": "2025-10-28T00:00:00Z",
  "updated": "2025-10-28T00:00:00Z"
}
```

#### 8.2 REST Endpoints

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/settings` | Get user settings. Creates default settings if none exist. |
| PATCH | `/api/settings` | Update settings. Partial updates supported (only send fields to change). |

---

## 3. Database Migrations

Pocketbase supports programmatic collection creation. We'll write a migration in Go that runs on server start:

```go
// internal/migrations/001_initial_schema.go
func init() {
    migrations.Register(func(app core.App) error {
        // Create conversations collection
        // Create messages collection
        // Create tasks collection
        // Create skills collection
        // Create knowledge collection
        // Create provider_configs collection
        // Create settings collection
        // Seed official skills
        return nil
    }, func(app core.App) error {
        // Rollback: delete collections
        return nil
    })
}
```

Each collection defines:
- Fields with types and validation rules
- List/detail/create/update/delete API rules (ownership-based: `@request.auth.id = user`)
- Indexes (user_id + created for conversations, user_id + type for tasks, full-text search indexes for knowledge)

---

## 4. WebSocket Architecture (Detailed)

### 4.1 Connection Lifecycle

```
Client connects to WS /api/ws/chat
    │
    ▼
Server upgrades HTTP → WS
    │
    ▼
Client sends auth token (first message or query param)
    │
    ▼
Server validates JWT, creates Client struct, registers with Hub
    │
    ▼
Client sends { "type": "join", "conversation_id": "abc" }
    │
    ▼
Server validates conversation ownership, adds to room
    │
    ▼
Client sends { "type": "message", ... }
    │
    ▼
Server processes → streams tokens → broadcasts to room
    │
    ▼ (connection drops / client disconnects)
Server removes from room + hub, cleans up
```

### 4.2 gorilla/websocket Setup

```go
var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin: func(r *http.Request) bool {
        // In dev, allow all origins. In prod, check against CORS config.
        return true
    },
}
```

### 4.3 Concurrency Model

- **Hub**: single goroutine with channel-based operations (register, unregister, broadcast)
- **Per-client read pump**: goroutine that reads from WS and sends to hub
- **Per-client write pump**: goroutine that reads from hub and writes to WS
- **Per-stream goroutine**: spawned when AI streaming starts, sends tokens to client's send channel

### 4.4 Error Handling & Recovery

- Auto-reconnect: if WS drops, client can rejoin with `{ "type": "reconnect", "conversation_id": "...", "last_token_index": 42 }`
- Rate limiting: max N messages per second per user
- Provider errors: broadcast `{ "type": "error" }` to room, don't crash the connection
- Dead connection cleanup: ping/pong with timeout (30s)

---

## 5. API Conventions

### 5.1 Standard Response Format

**Success:**
```json
{
  "data": { ... },           // single object or array
  "meta": {
    "page": 1,
    "per_page": 50,
    "total": 120
  }
}
```

**Error:**
```json
{
  "error": {
    "code": "not_found",
    "message": "Task not found",
    "details": null
  }
}
```

### 5.2 HTTP Status Codes

| Code | Usage |
|------|-------|
| 200 | Success (GET, PATCH) |
| 201 | Created (POST) |
| 204 | No Content (DELETE) |
| 400 | Bad Request (validation error) |
| 401 | Unauthorized (missing/invalid token) |
| 403 | Forbidden (wrong ownership) |
| 404 | Not Found |
| 429 | Rate Limited |
| 500 | Internal Server Error |

### 5.3 Pagination

All list endpoints support:
- `?page=1&perPage=50` (pagination)
- `?sort=-created` (sort by field, `-` prefix for descending)
- `?filter=field:value` (field filtering)

These are passed through to Pocketbase's built-in filtering where possible, or handled in custom logic.

---

## 6. Frontend ↔ Backend Contract Notes

### 6.1 Existing Frontend Patterns to Support

| Frontend Widget | Backend API Dependency |
|----------------|----------------------|
| `HomeScreen` (task list) | `GET /api/tasks?type=...` |
| `FilterRow` | `?type=Agent|Scheduled|Favorites` query param |
| `ChatScreen` (suggestions) | `GET /api/chat/suggestions` (or static JSON → seed in DB) |
| `ModelSelector` | `GET /api/models` |
| `ChatInputArea` | `POST /api/conversations/:id/messages` + WS streaming |
| `SuggestionCarousel` | `GET /api/chat/suggestions` |
| `LibraryScreen` (Skills tab) | `GET /api/skills`, `PATCH /api/skills/:id`, `POST /api/skills` |
| `LibraryScreen` (Knowledge tab) | `GET /api/knowledge`, `POST /api/knowledge`, `PATCH /api/knowledge/:id` |
| `ProfileScreen` (providers) | `GET /api/providers`, `POST /api/providers` |
| `ProfileScreen` (settings) | `GET /api/settings`, `PATCH /api/settings` |
| `ProfileScreen` (logout) | Client-side token clear + `POST /api/auth/logout` |
| `ArchieFab` → ChatScreen | Navigation + `POST /api/conversations` |

### 6.2 Frontend Mock Data That Should Be Replaced

| Current Mock | Replace With |
|---|---|
| `assets/data/dashboard.json` | `GET /api/tasks` |
| `assets/data/chat_suggestions.json` | Seeded in DB or `GET /api/chat/suggestions` |
| `mockTasks` in `agent_task.dart` | `GET /api/tasks` |
| Hardcoded skills in `Notifier` | `GET /api/skills` |
| Hardcoded knowledge in `Notifier` | `GET /api/knowledge` |
| Hardcoded providers in `profile_screen.dart` | `GET /api/providers` |

---

## 7. Testing Strategy

### 7.1 Unit Tests

| Package | Tests |
|---------|-------|
| `internal/auth` | Token generation, validation, refresh |
| `internal/tasks` | CRUD logic, filtering, ownership checks |
| `internal/chat` | Message creation, conversation management |
| `internal/providers` | Request building, response parsing, model name resolution |
| `internal/server` | Middleware (CORS, auth), route registration |
| `internal/common` | Validation helpers, error formatting |

### 7.2 Integration Tests

- HTTP tests using `httptest.Server` with a test Pocketbase instance
- Test each endpoint: happy path, auth errors, validation errors, ownership enforcement
- WebSocket tests with gorilla/websocket test helpers

### 7.3 Test Data

- Seed a test Pocketbase instance with known data
- Use `testify/assert` + `testify/require` for assertions
- Table-driven tests for handler logic

---

## 8. Development Workflow

### 8.1 Makefile Targets

```makefile
run         # Start server with hot-reload (air)
build       # Build binary
test        # Run all tests
test-watch  # Run tests on file changes
migrate     # Run Pocketbase migrations
seed        # Seed development data (official skills, sample tasks)
lint        # golangci-lint
```

### 8.2 Environment Files

```
.env.example    # Template with placeholder values
.env.dev        # Development overrides (auto-loaded if exists)
.env            # Not committed
```

### 8.3 First Run Sequence

1. `cp .env.example .env.dev`
2. `make run` → starts server on `:8090`
3. Server auto-creates Pocketbase data directory
4. Migrations create all collections
5. Seed script populates official skills + sample data
6. Visit `http://localhost:8090/_/` for Pocketbase Admin UI

---

## 9. Security Considerations

| Concern | Mitigation |
|---------|-----------|
| API key exposure | Encrypted at rest (AES-256-GCM), never in response payloads |
| JWT theft | Short-lived tokens (15 min) + refresh tokens (7 day). HTTPS enforced. |
| Auth bypass | Middleware checks JWT on all protected routes. Ownership checks on all CRUD operations. |
| SQL injection | Pocketbase uses parameterized queries internally. Custom queries use prepared statements. |
| Rate limiting | Per-IP and per-user rate limits on auth endpoints and chat messages. |
| CORS | Restricted to configured origins. Not wildcard in production. |
| WS origin check | `CheckOrigin` verifies against CORS whitelist in production. |
| Input validation | All inputs validated server-side. Pocketbase field rules as second layer. |

---

## 10. Future Considerations (Post-MVP)

- **File uploads**: Use Pocketbase's built-in file storage for attachments
- **Push notifications**: Send via WebSocket when tasks complete or require attention
- **Multi-user conversations**: Expand WebSocket rooms to support shared conversations
- **Admin dashboard**: Pocketbase Admin UI or custom admin panel for user management
- **Skill registry sync**: Periodically fetch official skills from AgentSkills.io API
- **Knowledge RAG**: Use embeddings + vector search for more relevant knowledge injection
- **Usage tracking**: Track tokens used per user, enforce tier limits
- **Offline support**: Frontend caches tasks/conversations locally via Flutter Hive

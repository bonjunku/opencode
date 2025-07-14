# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OpenCode is a Go-based CLI application that provides a terminal-based AI assistant for developers. It offers an interactive TUI (Terminal User Interface) for conversing with various AI models to help with coding tasks, debugging, and development workflows.

## Development Commands

### Build and Run
```bash
# Build the application
go build -o opencode

# Run the application
./opencode

# Run with debug logging
./opencode -d

# Run in specific directory
./opencode -c /path/to/project

# Run non-interactive mode with prompt
./opencode -p "Your prompt here"

# Run with JSON output format
./opencode -p "Your prompt here" -f json
```

### Development Tools
```bash
# Generate SQL code from queries (uses sqlc)
sqlc generate

# Run tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Check for vulnerabilities
go list -json -deps | nancy sleuth

# Format code
go fmt ./...

# Run linter (requires golangci-lint)
golangci-lint run

# Tidy dependencies
go mod tidy
```

### Database
- Uses SQLite with sqlc for type-safe database operations
- Migrations are in `internal/db/migrations/`
- SQL queries are in `internal/db/sql/`
- Run `sqlc generate` after modifying SQL files to regenerate Go code

## Architecture

### Core Components

#### Application Layer (`internal/app/`)
- `app.go`: Main application orchestration, handles both interactive and non-interactive modes
- `lsp.go`: Language Server Protocol integration initialization

#### Configuration (`internal/config/`)
- `config.go`: Centralized configuration management using Viper
- Supports multiple AI providers (OpenAI, Anthropic, Gemini, Groq, Azure, AWS Bedrock, etc.)
- Configuration files: `~/.opencode.json`, `$XDG_CONFIG_HOME/opencode/.opencode.json`, or `./.opencode.json`

#### Database Layer (`internal/db/`)
- SQLite database with sqlc-generated type-safe queries
- Models for sessions, messages, and file history
- Automatic migrations on startup

#### LLM Integration (`internal/llm/`)
- `agent/`: AI agent orchestration and tool management
- `models/`: Model definitions and provider mappings
- `prompt/`: Prompt engineering for different agent types (coder, summarizer, task, title)
- `provider/`: Implementation for different AI providers
- `tools/`: Built-in tools (bash, file operations, grep, etc.)

#### Terminal UI (`internal/tui/`)
- Built with Bubble Tea framework
- `components/`: Reusable UI components (chat, dialog, logs)
- `layout/`: Layout management and containers
- `page/`: Different application pages (chat, logs)
- `theme/`: Theming system with multiple color schemes

#### LSP Integration (`internal/lsp/`)
- Language Server Protocol client implementation
- Provides diagnostics and code intelligence
- Configurable per-language server support

#### Tools System
The AI assistant has access to various tools:
- File operations: `glob`, `grep`, `ls`, `view`, `write`, `edit`, `patch`
- System: `bash` for shell commands
- Web: `fetch` for HTTP requests
- Code search: `sourcegraph` for public repository search
- Sub-agents: `agent` for delegating tasks
- Diagnostics: LSP-based error checking

### Key Design Patterns

#### Service Layer
Most business logic is organized into services:
- `session.Service`: Session management
- `message.Service`: Message handling
- `permission.Service`: Permission system for tool access
- `history.Service`: File change tracking

#### Pub/Sub System (`internal/pubsub/`)
Event-driven architecture for communication between components:
- Services publish events
- TUI subscribes to events for real-time updates
- Prevents tight coupling between layers

#### Agent System
- Different agent types for different tasks (coder, summarizer, task, title)
- Configurable models and token limits per agent
- Tool access through permission system

## Configuration

### Environment Variables
- `ANTHROPIC_API_KEY`: Claude models
- `OPENAI_API_KEY`: OpenAI models
- `GEMINI_API_KEY`: Google Gemini models
- `GITHUB_TOKEN`: GitHub Copilot models
- `GROQ_API_KEY`: Groq models
- `AZURE_OPENAI_ENDPOINT` + `AZURE_OPENAI_API_KEY`: Azure OpenAI
- `AWS_*` credentials: AWS Bedrock
- `SHELL`: Default shell for bash tool

### Configuration Structure
The configuration supports:
- Multiple AI providers with API keys
- Agent-specific model assignments
- Shell configuration for tool execution
- LSP server configuration per language
- MCP (Model Context Protocol) server integration
- Custom context paths for project-specific instructions

## Development Workflow

1. **Configuration**: Application loads config from multiple sources (env vars, config files)
2. **Database**: SQLite connection with automatic migrations
3. **Services**: Core services initialize (sessions, messages, permissions, etc.)
4. **LSP**: Language servers start in background
5. **MCP**: External tools load via Model Context Protocol
6. **UI**: TUI starts with pub/sub event system
7. **Agent**: AI agent handles user requests with tool access

## Important Notes

- All file operations go through the permission system
- LSP integration provides real-time diagnostics
- Session management supports conversation history and summarization
- The tool system is extensible via MCP servers
- Configuration is hot-reloadable for some settings
- Auto-compact feature manages long conversations by summarizing context

## Testing

- Unit tests for individual components
- Integration tests for service interactions
- LSP protocol tests for language server communication
- Tool tests for AI assistant capabilities
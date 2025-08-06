# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is `numscan`, a Go REST API service for batch dialing phone numbers via FreeSWITCH ESL (Event Socket Library). The application has been transformed from a CLI tool to a full-featured web API that provides phone number management and scanning functionality through HTTP endpoints.

## Core Architecture

### API Service Structure
- **Entry Point**: `main.go` initializes the HTTP server
- **API Framework**: Uses Chi router with Huma v2 for OpenAPI specification and validation
- **Server Core**: `internal/server/server.go` contains the main server initialization and routing
- **Database Layer**: PostgreSQL with GORM ORM and GORM Gen for type-safe queries
- **Legacy CLI**: Original CLI commands remain in `cmd/` for backwards compatibility

### Key Components

#### REST API Endpoints
**Phone Number Management:**
- `POST /api/v1/numbers` - Add phone numbers to the database
- `GET /api/v1/numbers` - Query phone numbers with pagination and filtering

**Scan Job Management:**
- `POST /api/v1/scan` - Create a new scanning job
- `GET /api/v1/scan/{jobId}` - Get scan job status and results

#### Queue Management (River)
- **River Queue System**: Asynchronous job processing for phone number scanning
- **River UI**: Web-based monitoring interface at `/admin/river` (Basic Auth: admin/river)
- **Job Workers**: Background processing of phone number dialing tasks

#### Database Models (`internal/models/`)
- **PhoneNumber**: Stores phone numbers with processing status and call results
- **ProcessingStatus**: Enum for tracking number processing states (initial, pending, processing, dialing, completed)
- **CallResult**: Enum for call outcomes (answered, no_answer, busy, failed, timeout)

#### GORM Integration
- **Type-Safe Queries**: Uses GORM Gen for generating type-safe database query methods
- **Auto Migration**: Database schema is automatically managed
- **Connection Management**: PostgreSQL connection with configuration support

#### Event-Driven Call Handling (Legacy)
The FreeSWITCH ESL integration remains available through the scanner package:
- `CHANNEL_CREATE`: Establishes UUID-to-number mapping
- `CHANNEL_ANSWER`: Marks calls as answered
- `CHANNEL_HANGUP`: Finalizes call results with hangup causes

## Build and Development Commands

### API Server Operations
```bash
# Build the API server
go build -o numscan .

# Run the API server (default port 8080)
./numscan
# or with custom port
PORT=3000 ./numscan

# Run tests
go test ./...

# Format code  
go fmt ./...

# Static analysis
go vet ./...
```

### Database Operations
```bash
# Generate GORM models and queries
go run tools/gen/main.go

# Database migration is automatic on server startup
```

### Cross-Platform Builds
Use the provided Makefile for building across multiple platforms:
```bash
# Build for all supported platforms
make build-all

# Clean build artifacts
make clean
```

Supported platforms: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64, windows/arm64

### API Documentation
When the server is running, access:
- **API Documentation**: `http://localhost:8080/docs`
- **OpenAPI Specification**: `http://localhost:8080/openapi.json`
- **River Queue UI**: `http://localhost:8080/admin/river` (Basic Auth: admin/river)

## Key Dependencies

### API & Web Framework
- **github.com/go-chi/chi/v5**: HTTP router and middleware
- **github.com/danielgtaylor/huma/v2**: OpenAPI 3.0 framework with automatic validation and documentation
- **github.com/go-chi/render**: JSON rendering utilities

### Database
- **gorm.io/gorm**: ORM library
- **gorm.io/driver/postgres**: PostgreSQL driver for GORM
- **gorm.io/gen**: Code generator for type-safe queries
- **github.com/google/uuid**: UUID generation

### Queue System
- **github.com/riverqueue/river**: Background job processing system
- **riverqueue.com/riverui**: Web-based job monitoring interface
- **github.com/jackc/pgx/v5**: PostgreSQL driver for River queue

### Configuration
- **github.com/knadh/koanf/v2**: Configuration management (TOML files + environment variables)

### FreeSWITCH Integration (Legacy)
- **github.com/percipia/eslgo**: FreeSWITCH ESL client library

### Development & Testing
- **github.com/spf13/cobra**: CLI framework (for legacy commands)
- **github.com/stretchr/testify**: Testing utilities

## Critical Implementation Details

### API Architecture
The application uses a layered architecture:
1. **HTTP Layer**: Chi router handles HTTP requests and middleware
2. **API Layer**: Huma v2 provides OpenAPI validation and documentation
3. **Queue Layer**: River provides asynchronous job processing for background tasks
4. **Service Layer**: Business logic in handlers (internal/server/handlers.go)
5. **Data Layer**: GORM with generated type-safe queries (internal/query/)
6. **Model Layer**: Database models with business methods (internal/models/)

### Database Design
- **PhoneNumber Model**: Central entity with processing status tracking
- **Status States**: Tracks progression from initial → pending → processing → dialing → completed
- **Call Results**: Stores final outcomes (answered, no_answer, busy, failed, timeout)
- **Audit Fields**: CreatedAt, UpdatedAt, DeletedAt for change tracking
- **Unique Constraints**: Prevents duplicate phone number entries

### Configuration Management
Configuration supports both TOML files and environment variables:
```toml
[database]
host = "localhost"
port = "5432"
user = "postgres"
password = "password"
dbname = "numscan"

[app]
ring_time = 30
concurrency = 1
dial_delay = 100
```

Environment variables use `NUMSCAN_` prefix (e.g., `NUMSCAN_DATABASE_HOST`)

### Legacy Event Processing (CLI Mode)
When using the scanner package directly:
1. Dial job creates call with unique UUID
2. `CHANNEL_CREATE` establishes UUID-number mapping
3. `CHANNEL_ANSWER` marks call as answered (state only)
4. `CHANNEL_HANGUP` writes final result based on answer state and hangup cause

### Security & Middleware
- **CORS**: Configurable cross-origin request handling
- **Request Logging**: All API requests are logged with operation IDs
- **Error Handling**: Unified error response format
- **Input Validation**: Automatic validation via Huma's OpenAPI schemas
- **Panic Recovery**: Graceful handling of runtime panics
```

## Memories

- `to memorize`
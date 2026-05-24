# CasGists Development Notes

## Project Overview
CasGists is a production-ready, self-hosted GitHub Gist alternative designed for non-technical self-hosters and small/medium businesses. It provides a secure, feature-rich platform for managing code snippets with enterprise-grade security and ease of use.

## Current Status
- **Version**: 1.0.0
- **Status**: Production Ready
- **Build**: ✓ Single static binary (CGO_ENABLED=0)
- **Docker**: ✓ Multi-platform images (60.2MB)
- **Tests**: ✓ All integration tests passing
- **Logging**: ✓ Pretty console + Apache Common Log Format

## Key Design Principles
1. **Single Static Binary**: Deploy with zero dependencies (CGO_ENABLED=0)
2. **Port Range 64000-64999**: Avoids conflicts with common services
3. **Security-First**: Enterprise features invisible to regular users
4. **Mobile-First**: Responsive design optimized for all devices
5. **"Never Die" Principle**: Maximum functionality under all conditions

## Repository Structure

```
casgists/
├── src/                    # All source code
│   ├── cmd/casgists/      # Main application entry point
│   ├── internal/          # Internal packages
│   │   ├── server/        # Echo server and routes
│   │   ├── database/      # GORM models and migrations (embedded)
│   │   ├── auth/          # JWT, 2FA, WebAuthn
│   │   ├── config/        # Configuration management
│   │   └── ...
│   └── web/               # Web assets (embedded in binary)
│       ├── static/        # CSS, JS, images
│       └── templates/     # HTML templates
├── scripts/               # Production/install scripts
│   ├── deploy.sh          # Deployment automation
│   ├── generate-icons.sh  # PWA icon generation
│   ├── health-check.sh    # Health monitoring
│   ├── maintenance.sh     # Maintenance tasks
│   └── optimize.sh        # Performance optimization
├── tests/                 # Development/test scripts
│   ├── *.go              # Go test files
│   └── *.sh              # Test automation scripts
├── binaries/              # Built binaries (gitignored)
├── docs/                  # Documentation (ReadTheDocs with Dracula theme)
├── data/                  # Runtime data directory (gitignored)
├── ssl/                   # Runtime SSL certificates (gitignored)
├── Makefile              # Simplified build system
├── Dockerfile            # Alpine-based multi-stage build
├── docker-compose.yml    # Docker Compose configuration
├── release.txt           # Semantic version (1.0.0)
├── VERSION               # Version file
└── CLAUDE.md            # This file (development notes)
```

## Build System

### Makefile Targets (BASE SPEC Compliant)
```bash
make build    # Build for ALL platforms + host binary to ./binaries
              # Platforms: linux, darwin, windows, freebsd, openbsd, netbsd
              # Architectures: amd64, arm64 (12 total platform combinations)
              # Binary naming: casgists-{os}-{arch}
              # Strips linux binaries automatically

make release  # Create GitHub release with auto-increment version
              # Reads version from ./release.txt
              # Auto-increments patch version
              # Packages binaries (.tar.gz for Unix, .zip for Windows)
              # Generates SHA256SUMS.txt
              # Creates GitHub release using gh CLI

make docker   # Build and push multi-arch images to ghcr.io
              # Platforms: linux/amd64, linux/arm64
              # Tags: ghcr.io/casapps/casgists:VERSION and :latest
              # Requires: docker buildx and ghcr.io authentication

make test     # Run all Go tests with race detector
make clean    # Clean build artifacts
make version  # Show current version from ./release.txt
make help     # Show available targets
```

### Version Management
- Version is read from `./release.txt` (semantic versioning)
- Version is embedded in binary at build time via ldflags
- Docker images are tagged with version from release.txt

### Build Output
- Binary output: `./binaries/casgists` (host platform, 33MB static binary)
- Platform binaries: `./binaries/casgists-{os}-{arch}` (31-33MB each)
  - linux: amd64, arm64
  - darwin: amd64, arm64
  - windows: amd64.exe, arm64.exe
  - freebsd: amd64, arm64
  - openbsd: amd64, arm64
  - netbsd: amd64, arm64
- Docker image: `ghcr.io/casapps/casgists:1.0.0` and `:latest` (60MB Alpine-based)
- Release packages: `./releases/` (tar.gz for Unix, zip for Windows + checksums)

## Architecture Highlights

### Database
- Multi-database support via GORM (SQLite default, PostgreSQL, MySQL)
- Migrations embedded in binary at `src/internal/database/migrations/`
- Auto-migration on startup
- Connection pooling

### Authentication
- JWT with refresh tokens
- 2FA (TOTP)
- WebAuthn/Passkeys
- Session management

### Git Integration
- go-git library (no external Git dependency)
- Full Git operations (clone, commit, push, pull)
- Branch management
- Git history

### Search
- SQLite FTS5 (default)
- Redis/Valkey fallback for better performance
- Full-text search on gist content

### Caching
- In-memory LRU cache
- Redis fallback for distributed caching

### Logging System
- **Console Output**: Pretty format for human readability
  - Format: `2025-10-18T20:35:44Z | 200 | 967.53µs | GET /api/v1/health`
  - No ANSI color codes
  - Clean, readable output
- **Access Log**: Apache Common Log Format
  - File: `/var/log/casgists/access.log`
  - Format: `127.0.0.1 - - [18/Oct/2025:20:36:47 +0000] "GET /favicon.ico HTTP/1.1" 200 1086`
  - Standard format for log analyzers
- **Server Log**: Server events and migrations
  - File: `/var/log/casgists/server.log`
  - Dual output: console + file
  - Migration status, startup messages
- **GORM**: Silent mode (no SQL spam in production)
  - Only logs errors
  - Debug mode available via CASGISTS_DEBUG=true

### Path Variables
Smart path substitution system:
- `${DATA_DIR}` - Expands to data directory path
- `${CONFIG_DIR}` - Expands to config directory path
- `${LOG_DIR}` - Expands to log directory path

## Key Features Implemented

1. **Embedded Assets**
   - Web templates embedded at build time
   - Static assets (CSS, JS, images) embedded
   - Database migrations embedded
   - No external file dependencies

2. **Server Configuration**
   - Port range: 64000-64999 (configurable)
   - Automatic port selection if not specified
   - Network detection (IP/FQDN instead of localhost)
   - HTTPS support with automatic certificate management

3. **Security Features**
   - JWT authentication with refresh token rotation
   - CSRF protection on all forms
   - Rate limiting on API endpoints
   - Secure password hashing (Argon2id)
   - HMAC-signed webhooks
   - Content Security Policy (CSP)

4. **API System**
   - RESTful API at `/api/v1/`
   - Health endpoints (`/api/v1/health`, `/api/v1/health/enhanced`)
   - Gist CRUD operations
   - Organization and team management
   - Swagger UI documentation

## Important Implementation Details

1. **UUID Primary Keys**: Used throughout for security and portability
2. **Soft Deletes**: All models support soft deletion via GORM
3. **Path Substitution**: Variables like `${DATA_DIR}` in configurations
4. **Network Detection**: Automatic IP/FQDN detection for URLs
5. **Error Handling**: Comprehensive error handling with user-friendly messages
6. **No External Dependencies**: Single binary includes everything

## Testing

### Current Test Status
- ✓ All 11 integration endpoints passing
- ✓ Health check endpoints working
- ✓ SQLite and PostgreSQL tested
- ✓ Docker build and run tested
- ✓ Binary build tested (33MB static binary)

### Test Endpoints
1. GET `/api/v1/health` - Basic health check
2. GET `/api/v1/health/enhanced` - Detailed health with metrics
3. GET `/api/v1/gists` - List public gists
4. GET `/` - Home page
5. GET `/install.sh` - CLI installer script
6. GET `/manifest.json` - PWA manifest
7. GET `/robots.txt` - Search engine directives
8. GET `/service-worker.js` - PWA service worker
9. GET `/favicon.ico` - Site favicon

### Test Commands
```bash
make test          # Run all tests with race detector
make build         # Build binary and verify
make docker        # Build Docker image and verify
```

## Performance Optimizations

- Pagination on all list endpoints
- Lazy loading for large gists
- Efficient file storage structure
- Background job processing
- Connection pooling for databases
- In-memory caching with LRU eviction
- Static asset compression

## Deployment

### Recommended Setup
1. **Small Deployments** (<1000 users): SQLite
2. **Medium Deployments** (1000-10000 users): PostgreSQL
3. **Large Deployments** (>10000 users): PostgreSQL + Redis
4. **Enterprise**: PostgreSQL + Redis + S3-compatible storage

### Docker Deployment
```bash
docker run -d \
  --name casgists \
  -p 64080:64080 \
  -v casgists_data:/data \
  -v casgists_logs:/var/log/casgists \
  -e CASGISTS_DB_TYPE=sqlite \
  -e CASGISTS_DB_DSN=/data/casgists.db \
  casapps/casgists:1.0.0
```

### Binary Deployment
```bash
./binaries/casgists \
  --port 64080 \
  --data-dir /var/lib/casgists \
  --log-dir /var/log/casgists
```

## Development Workflow

### Quick Development
```bash
# Clone and build
git clone https://github.com/casapps/casgists.git
cd casgists
make build

# Run the application
./binaries/casgists

# Application starts on random port 64000-64999
# Visit http://localhost:<port> shown in console
```

### Docker Development
```bash
# Build Docker image
make docker

# Run with Docker Compose
docker-compose up -d

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

### Testing Changes
```bash
# Run tests
make test

# Build and test binary
make clean build
./binaries/casgists --version

# Build and test Docker
make docker
docker run --rm casapps/casgists:latest --version
```

## Environment Variables

### Core Configuration
- `CASGISTS_DB_TYPE`: Database type (sqlite, postgresql, mysql)
- `CASGISTS_DB_DSN`: Database connection string
- `CASGISTS_DATA_DIR`: Data directory path (default: /data)
- `CASGISTS_LOG_DIR`: Log directory path (default: /var/log/casgists)
- `CASGISTS_SERVER_PORT`: Server port (default: random 64000-64999)

### Security
- `CASGISTS_SECRET_KEY`: JWT signing key (auto-generated if not set)
- `CASGISTS_SESSION_SECRET`: Session encryption key

### Features
- `CASGISTS_FEATURES_REGISTRATION`: Enable user registration (default: true)
- `CASGISTS_FEATURES_ORGANIZATIONS`: Enable organizations (default: true)
- `CASGISTS_DEBUG`: Enable debug mode (default: false)

## Known Issues & Solutions

### Issue: Migrations failing
**Solution**: Ensure migrations are embedded correctly in `src/internal/database/migrations/`

### Issue: Port already in use
**Solution**: Use `CASGISTS_SERVER_PORT=0` for automatic port selection from range 64000-64999

### Issue: Database locked
**Solution**: Check no other processes are accessing the SQLite database

### Issue: Log files not created
**Solution**: Ensure log directory exists and has write permissions for the casgists user

## Release Process

1. Update `release.txt` with new version (e.g., 1.0.1)
2. Update `VERSION` file to match
3. Update `CHANGELOG.md` with release notes
4. Run `make clean build` to test build
5. Run `make test` to verify tests pass
6. Run `make docker` to build Docker image
7. Tag release in Git: `git tag v1.0.1`
8. Push to repository: `git push && git push --tags`
9. CI/CD will build multi-platform binaries and Docker images
10. Create GitHub release with binaries attached

## Contributing Guidelines

1. Keep code simple and readable
2. Follow Go conventions (gofmt, golint)
3. Write tests for new features
4. Update documentation
5. Ensure single binary principle (no external dependencies)
6. Maintain backward compatibility
7. Use semantic versioning

## Support & Documentation

- **Documentation**: https://casgists.readthedocs.io (Dracula theme)
- **GitHub Issues**: Bug reports and feature requests
- **GitHub Discussions**: Questions and community support
- **ReadTheDocs**: Comprehensive documentation

## Future Considerations

- GraphQL API addition
- Real-time collaboration features
- Plugin system for extensions
- Advanced analytics dashboard
- Mobile applications (iOS, Android)
- Desktop applications (Electron)

---

**Remember**: This is designed for non-technical users. Keep the UI simple, error messages helpful, and provide sensible defaults for all configurations.

**Last Updated**: 2025-10-18
**Status**: Production Ready (v1.0.0)

## BASE SPEC Compliance

### ✅ Fully Implemented Requirements

**Repository Structure:**
- ✓ All source in `./src`
- ✓ Production/install scripts in `./scripts`
- ✓ Development/test scripts in `./tests`
- ✓ Built binaries in `./binaries`
- ✓ Clean, organized root directory

**Build System:**
- ✓ Semantic versioning via `./release.txt`
- ✓ Auto-increment on release
- ✓ Multi-platform builds (12 combinations)
- ✓ Binary naming: `{projectname}-{os}-{arch}`
- ✓ Strip linux binaries
- ✓ Single static binary (CGO_ENABLED=0)

**Makefile Targets:**
- ✓ `make build` - Build all platforms + host binary
- ✓ `make release` - GitHub release with gh CLI
- ✓ `make docker` - Push to ghcr.io/casapps/casgists
- ✓ `make test` - Run all tests

**Docker:**
- ✓ Alpine-based multi-stage build
- ✓ OCI metadata labels
- ✓ Bash + curl in final stage
- ✓ Binary in `/usr/local/bin`
- ✓ Internal port 80
- ✓ Directories: `/data`, `/config`, `/var/log/casgists`
- ✓ SQLite DB in `/data/db`

**Docker Compose:**
- ✓ No version field
- ✓ No build definition (uses pre-built image)
- ✓ Custom network `casgists` with external: false
- ✓ Volume paths: `./rootfs/data/`, `./rootfs/config/`, `./rootfs/db/`
- ✓ Port mapping: `64xxx:80`
- ✓ Production: `172.17.0.1:{port}:80`

**Application:**
- ✓ Database as config (no server config file)
- ✓ SystemConfig model for all configuration
- ✓ 8-step setup wizard
- ✓ First user flow with admin account
- ✓ Port range 64000-64999
- ✓ Automatic port selection
- ✓ Pretty console output with emojis
- ✓ Apache Common Log Format for access.log
- ✓ No ANSI/emoji in log files
- ✓ Path variables: `${DATA_DIR}`, `${CONFIG_DIR}`, `${LOG_DIR}`

**Security:**
- ✓ User creation with UID/GID 1001 (system user range 100-999)
- ✓ Privilege escalation support
- ✓ All validation and sanitization
- ✓ Security-first, mobile-first design

### 📝 Notes

**GitHub Release:** Requires `gh` CLI to be installed and authenticated
**Docker Push:** Requires authentication to ghcr.io via `docker login ghcr.io`
**Version Auto-Increment:** `make release` auto-increments patch version (1.0.0 → 1.0.1)
**Platform Support:** All major platforms supported for maximum deployment flexibility


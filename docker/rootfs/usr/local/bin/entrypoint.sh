#!/bin/bash

# CasGists Docker Entrypoint Script - BASE SPEC Compliant

set -e

# Default values per BASE SPEC
CASGISTS_SERVER_PORT=${CASGISTS_SERVER_PORT:-80}
CASGISTS_DB_TYPE=${CASGISTS_DB_TYPE:-sqlite}
CASGISTS_DB_DSN=${CASGISTS_DB_DSN:-/data/db/casgists.db}
CASGISTS_DATA_DIR=${CASGISTS_DATA_DIR:-/data}
CASGISTS_LOG_DIR=${CASGISTS_LOG_DIR:-/var/log/casgists}

# Create necessary directories
mkdir -p "$CASGISTS_DATA_DIR"
mkdir -p "$CASGISTS_DATA_DIR/db"
mkdir -p "$CASGISTS_LOG_DIR"
mkdir -p /config

# Wait for database if using PostgreSQL or MySQL
if [ "$CASGISTS_DB_TYPE" = "postgres" ] || [ "$CASGISTS_DB_TYPE" = "postgresql" ] || [ "$CASGISTS_DB_TYPE" = "mysql" ]; then
    echo "⏳ Waiting for database to be ready..."

    # Extract host and port from DSN
    DB_HOST=$(echo "$CASGISTS_DB_DSN" | sed -n 's/.*@\([^:\/]*\)[:\/]\([0-9]*\).*/\1/p')
    DB_PORT=$(echo "$CASGISTS_DB_DSN" | sed -n 's/.*@[^:\/]*[:\/]\([0-9]*\).*/\1/p')

    # Default ports if not found
    [ -z "$DB_PORT" ] && [ "$CASGISTS_DB_TYPE" = "postgres" ] && DB_PORT=5432
    [ -z "$DB_PORT" ] && [ "$CASGISTS_DB_TYPE" = "postgresql" ] && DB_PORT=5432
    [ -z "$DB_PORT" ] && [ "$CASGISTS_DB_TYPE" = "mysql" ] && DB_PORT=3306

    if [ -n "$DB_HOST" ] && [ -n "$DB_PORT" ]; then
        echo "📡 Checking connection to $DB_HOST:$DB_PORT..."

        # Wait up to 60 seconds for database
        for i in $(seq 1 60); do
            if timeout 2 bash -c "echo >/dev/tcp/$DB_HOST/$DB_PORT" 2>/dev/null; then
                echo "✅ Database is ready!"
                break
            fi
            echo "⏳ Waiting for database... ($i/60)"
            sleep 1
        done

        if ! timeout 2 bash -c "echo >/dev/tcp/$DB_HOST/$DB_PORT" 2>/dev/null; then
            echo "❌ ERROR: Database is not accessible after 60 seconds"
            exit 1
        fi
    fi
fi

# Generate secret key if not provided
if [ -z "$CASGISTS_SECRET_KEY" ]; then
    echo "🔐 Generating random secret key..."
    CASGISTS_SECRET_KEY=$(head -c 32 /dev/urandom | base64 | tr -d '\n=' | head -c 32)
    export CASGISTS_SECRET_KEY
    echo "✅ Generated secret key (save for future use)"
    echo "   CASGISTS_SECRET_KEY=$CASGISTS_SECRET_KEY"
fi

# Print startup info
echo ""
echo "🚀 Starting CasGists..."
echo "   Version: $(/usr/local/bin/casgists --version 2>/dev/null | tail -1 || echo 'unknown')"
echo "   Port: $CASGISTS_SERVER_PORT"
echo "   Database: $CASGISTS_DB_TYPE"
echo "   Data directory: $CASGISTS_DATA_DIR"
echo "   Log directory: $CASGISTS_LOG_DIR"
echo ""

# Execute the main command
exec "$@"

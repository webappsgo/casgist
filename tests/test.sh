#!/bin/bash

# CasGists Test Script
# This script performs basic tests to ensure the application is working correctly

set -e

echo "=== CasGists Test Script ==="
echo

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Test function
test_pass() {
    echo -e "${GREEN}✓${NC} $1"
}

test_fail() {
    echo -e "${RED}✗${NC} $1"
    exit 1
}

# Check if binary exists
if [ ! -f "./casgist" ]; then
    echo "Building CasGists..."
    go build -o casgist cmd/casgist/main.go || test_fail "Build failed"
    test_pass "Build successful"
fi

# Test 1: Version check
echo
echo "Test 1: Version check"
./casgist --version || test_fail "Version check failed"
test_pass "Version check passed"

# Test 2: Help command
echo
echo "Test 2: Help command"
./casgist --help > /dev/null || test_fail "Help command failed"
test_pass "Help command passed"

# Test 3: Configuration validation
echo
echo "Test 3: Configuration"
export CASGISTS_DATABASE_TYPE=sqlite
export CASGISTS_DATABASE_DSN=./test_data/test.db
export CASGISTS_SECURITY_SECRET_KEY=test-secret-key-for-testing-only-32chars
export CASGISTS_SERVER_PORT=8765
test_pass "Configuration set"

# Test 4: Database connection
echo
echo "Test 4: Database initialization"
mkdir -p ./test_data

# Start server in background
echo "Starting server..."
./casgist > test_server.log 2>&1 &
SERVER_PID=$!

# Wait for server to start
sleep 3

# Check if server is running
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server log:"
    cat test_server.log
    test_fail "Server failed to start"
fi
test_pass "Server started on port 8765"

# Test 5: Health check
echo
echo "Test 5: Health check"
curl -f -s http://localhost:8765/api/v1/health > /dev/null || test_fail "Health check failed"
test_pass "Health check passed"

# Test 6: API version
echo
echo "Test 6: API version endpoint"
VERSION_RESPONSE=$(curl -s http://localhost:8765/api/v1/version)
echo "Version response: $VERSION_RESPONSE"
test_pass "Version endpoint working"

# Test 7: Registration endpoint (should fail without valid data)
echo
echo "Test 7: Registration endpoint"
REGISTER_RESPONSE=$(curl -s -X POST http://localhost:8765/api/v1/auth/register \
    -H "Content-Type: application/json" \
    -d '{"username":"","email":"","password":""}' \
    -w "\nHTTP_CODE:%{http_code}")

HTTP_CODE=$(echo "$REGISTER_RESPONSE" | grep "HTTP_CODE:" | cut -d: -f2)
if [ "$HTTP_CODE" = "400" ]; then
    test_pass "Registration validation working (expected 400 for invalid data)"
else
    test_fail "Registration endpoint not responding correctly"
fi

# Test 8: Create a valid user
echo
echo "Test 8: Create test user"
REGISTER_RESPONSE=$(curl -s -X POST http://localhost:8765/api/v1/auth/register \
    -H "Content-Type: application/json" \
    -d '{
        "username": "testuser",
        "email": "test@example.com",
        "password": "TestPassword123!"
    }' \
    -w "\nHTTP_CODE:%{http_code}")

HTTP_CODE=$(echo "$REGISTER_RESPONSE" | grep "HTTP_CODE:" | cut -d: -f2)
if [ "$HTTP_CODE" = "201" ]; then
    test_pass "User registration successful"
else
    echo "Response: $REGISTER_RESPONSE"
    test_fail "User registration failed"
fi

# Test 9: Login
echo
echo "Test 9: User login"
LOGIN_RESPONSE=$(curl -s -X POST http://localhost:8765/api/v1/auth/login \
    -H "Content-Type: application/json" \
    -d '{
        "username": "testuser",
        "password": "TestPassword123!"
    }')

if echo "$LOGIN_RESPONSE" | grep -q "access_token"; then
    test_pass "Login successful"
    ACCESS_TOKEN=$(echo "$LOGIN_RESPONSE" | grep -o '"access_token":"[^"]*' | cut -d'"' -f4)
else
    echo "Response: $LOGIN_RESPONSE"
    test_fail "Login failed"
fi

# Test 10: Create a gist
echo
echo "Test 10: Create gist"
GIST_RESPONSE=$(curl -s -X POST http://localhost:8765/api/v1/gists \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $ACCESS_TOKEN" \
    -d '{
        "title": "Test Gist",
        "description": "This is a test gist",
        "visibility": "public",
        "files": [
            {
                "filename": "hello.go",
                "content": "package main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}"
            }
        ],
        "tags": ["test", "golang"]
    }' \
    -w "\nHTTP_CODE:%{http_code}")

HTTP_CODE=$(echo "$GIST_RESPONSE" | grep "HTTP_CODE:" | cut -d: -f2)
if [ "$HTTP_CODE" = "201" ]; then
    test_pass "Gist created successfully"
else
    echo "Response: $GIST_RESPONSE"
    test_fail "Gist creation failed"
fi

# Test 11: Search
echo
echo "Test 11: Search functionality"
SEARCH_RESPONSE=$(curl -s "http://localhost:8765/api/v1/search?q=test" \
    -H "Authorization: Bearer $ACCESS_TOKEN")

if echo "$SEARCH_RESPONSE" | grep -q "results"; then
    test_pass "Search endpoint working"
else
    echo "Response: $SEARCH_RESPONSE"
    test_fail "Search failed"
fi

# Cleanup
echo
echo "Cleaning up..."
kill $SERVER_PID 2>/dev/null || true
rm -rf ./test_data
rm -f test_server.log
test_pass "Cleanup complete"

echo
echo "=== All tests passed! ==="
echo
echo "CasGists is working correctly. You can now:"
echo "1. Run './casgist' to start the server"
echo "2. Access the web interface at http://localhost:3000"
echo "3. Use the API at http://localhost:3000/api/v1"
echo
echo "Default first user is admin. Check the docs for more info."
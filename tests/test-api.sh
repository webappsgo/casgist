#!/bin/bash

# Kill any existing processes
pkill -f casgist 2>/dev/null || true
sleep 2

# Start server
echo "Starting server..."
./build/casgist --config=configs/development.yaml > test-server.log 2>&1 &
SERVER_PID=$!

# Wait for server to start
echo "Waiting for server to start..."
sleep 5

# Get the port
PORT=$(grep "starting on port" test-server.log | tail -1 | awk '{print $NF}')
echo "Server started on port: $PORT"

# Test health endpoint
echo "Testing health endpoint..."
curl -s http://127.0.0.1:$PORT/health | jq .

# Test registration endpoint
echo "Testing registration endpoint..."
curl -X POST http://127.0.0.1:$PORT/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username": "testuser", "email": "test@example.com", "password": "Test123!@#"}' \
  -v 2>&1 | grep -E "(< HTTP|Bind error|{)"

# Check server logs
echo "Server logs:"
tail -20 test-server.log | grep -E "(Bind error|error)"

# Kill server
kill $SERVER_PID 2>/dev/null || true
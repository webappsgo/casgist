#!/bin/bash

# Kill any existing processes
pkill -f casgist 2>/dev/null || true
sleep 2

# Start server
echo "Starting server..."
./build/casgist --config=configs/development.yaml > server-test.log 2>&1 &
SERVER_PID=$!

# Wait for server to start
echo "Waiting for server to start..."
sleep 5

# Get the port
PORT=$(grep "starting on port" server-test.log | tail -1 | awk '{print $NF}')
echo "Server started on port: $PORT"
echo "Server PID: $SERVER_PID"

echo "Server is running. To stop it, run: kill $SERVER_PID"
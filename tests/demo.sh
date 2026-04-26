#!/bin/bash

# CasGists v1.0.0 Demo Script

echo "=== CasGists v1.0.0 Demo ==="
echo "This demo shows a working GitHub Gist alternative"
echo ""

# Clean up
rm -f demo.db demo.log
pkill -f casgist 2>/dev/null
sleep 1

# Start server in background
echo "Starting CasGists server..."
CASGISTS_DB_TYPE=sqlite CASGISTS_DB_DSN=demo.db ./build/casgist > demo.log 2>&1 &
SERVER_PID=$!
sleep 5

# Get the port from log
PORT=$(grep "starting on port" demo.log | awk '{print $NF}')
if [ -z "$PORT" ]; then
    echo "Error: Server failed to start"
    exit 1
fi

echo "Server started on port $PORT"
echo ""

BASE_URL="http://localhost:$PORT"

# 1. Register a user
echo "1. Registering new user..."
REGISTER_RESPONSE=$(curl -s -X POST $BASE_URL/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "username": "demouser",
    "email": "demo@example.com", 
    "password": "DemoPassword123!",
    "password_confirm": "DemoPassword123!"
  }')

TOKEN=$(echo $REGISTER_RESPONSE | grep -o '"access_token":"[^"]*' | cut -d'"' -f4)
if [ -z "$TOKEN" ]; then
    echo "Error: Failed to register user"
    kill $SERVER_PID
    exit 1
fi

echo "✓ User registered successfully"
echo ""

# 2. Create a gist
echo "2. Creating a code snippet..."
GIST_RESPONSE=$(curl -s -X POST $BASE_URL/api/v1/gists \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "title": "Hello World Examples",
    "description": "Hello World in different programming languages",
    "visibility": "public",
    "files": [
      {
        "filename": "hello.py",
        "content": "#!/usr/bin/env python3\n\ndef main():\n    print(\"Hello, World from Python!\")\n\nif __name__ == \"__main__\":\n    main()"
      },
      {
        "filename": "hello.js", 
        "content": "// JavaScript Hello World\nconsole.log(\"Hello, World from JavaScript!\");\n\n// Using arrow function\nconst greet = () => console.log(\"Greetings from ES6!\");\ngreet();"
      },
      {
        "filename": "hello.go",
        "content": "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, World from Go!\")\n}"
      }
    ]
  }')

GIST_ID=$(echo $GIST_RESPONSE | grep -o '"id":"[^"]*' | head -1 | cut -d'"' -f4)
if [ -z "$GIST_ID" ]; then
    echo "Error: Failed to create gist"
    echo "Response: $GIST_RESPONSE"
    kill $SERVER_PID
    exit 1
fi

echo "✓ Gist created with ID: $GIST_ID"
echo ""

# 3. List gists
echo "3. Fetching user's gists..."
curl -s -X GET $BASE_URL/api/v1/gists \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool | head -20
echo ""

# 4. View specific gist
echo "4. Viewing gist details..."
curl -s -X GET $BASE_URL/api/v1/gists/$GIST_ID | python3 -m json.tool | head -30
echo ""

# 5. Search gists
echo "5. Searching for 'Hello'..."
curl -s -X GET "$BASE_URL/api/v1/search?q=Hello" | python3 -m json.tool | head -20
echo ""

# 6. Show web interface
echo "6. Web interface available at:"
echo "   $BASE_URL"
echo "   $BASE_URL/login"
echo "   $BASE_URL/gists"
echo ""

echo "=== Demo Complete ==="
echo "Server running on PID $SERVER_PID"
echo "To stop server: kill $SERVER_PID"
echo ""
echo "Features demonstrated:"
echo "- User registration and authentication"
echo "- Creating multi-file gists"
echo "- Listing and viewing gists"
echo "- Search functionality"
echo "- RESTful API"
echo "- Web interface"
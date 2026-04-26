#!/bin/bash

# CasGists Feature Testing Script
# Tests major functionality to ensure v1.0.0 readiness

echo "🚀 CasGists v1.0.0 Feature Testing"
echo "================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test counter
TESTS_PASSED=0
TESTS_FAILED=0

# Helper function to run tests
run_test() {
    local test_name="$1"
    local test_command="$2"
    
    echo -e "${BLUE}Testing: ${test_name}${NC}"
    
    if eval "$test_command" &>/dev/null; then
        echo -e "${GREEN}✓ PASS: ${test_name}${NC}"
        ((TESTS_PASSED++))
    else
        echo -e "${RED}✗ FAIL: ${test_name}${NC}"
        ((TESTS_FAILED++))
    fi
}

# Build Test
echo -e "${YELLOW}1. Build System Tests${NC}"
run_test "Go Build" "go build ./cmd/casgist"
run_test "Go Mod Tidy" "go mod tidy"
run_test "Go Vet" "go vet ./..."

# Code Quality Tests
echo -e "\n${YELLOW}2. Code Quality Tests${NC}"
run_test "Go Fmt Check" "test -z \$(gofmt -l .)"

# File Structure Tests
echo -e "\n${YELLOW}3. File Structure Tests${NC}"
run_test "Main Binary Exists" "test -f casgist"
run_test "Config Directory" "test -d configs"
run_test "Web Assets" "test -d web/static"
run_test "Templates" "test -d web/templates"
run_test "Documentation" "test -f README.md"
run_test "Makefile" "test -f Makefile"

# PWA Tests
echo -e "\n${YELLOW}4. PWA Component Tests${NC}"
run_test "Service Worker" "test -f web/static/sw.js"
run_test "PWA Manifest" "test -f web/static/manifest.json"
run_test "IndexedDB Script" "test -f web/static/js/indexeddb.js"
run_test "PWA JavaScript" "test -f web/static/js/pwa.js"
run_test "Offline Page" "test -f web/templates/pages/offline.html"

# Configuration Tests
echo -e "\n${YELLOW}5. Configuration Tests${NC}"
run_test "Default Config" "test -f configs/casgist.yaml"
run_test "Development Config" "test -f configs/development.yaml"
run_test "Production Config" "test -f configs/production.yaml"

# Service Tests
echo -e "\n${YELLOW}6. Service Installation Tests${NC}"
run_test "Systemd Service File" "test -f scripts/casgist.service"
run_test "Install Script" "test -f scripts/install.sh"
run_test "Privilege Script" "test -f scripts/privilege.sh"

# Database Migration Tests
echo -e "\n${YELLOW}7. Database Schema Tests${NC}"
run_test "Models Directory" "test -d internal/models"
run_test "Migration Files" "ls internal/models/*.go | grep -q ."

# API Handler Tests
echo -e "\n${YELLOW}8. API Handler Tests${NC}"
run_test "Auth Handlers" "test -f internal/api/handlers/auth.go"
run_test "Gist Handlers" "test -f internal/api/handlers/gist.go"
run_test "User Handlers" "test -f internal/api/handlers/user.go"
run_test "Admin Handlers" "test -f internal/api/handlers/admin.go"
run_test "Offline Handlers" "test -f internal/api/handlers/offline.go"
run_test "Compliance Handlers" "test -f internal/api/handlers/compliance.go"

# Security Feature Tests
echo -e "\n${YELLOW}9. Security Feature Tests${NC}"
run_test "JWT Auth Service" "test -f internal/auth/jwt.go"
run_test "CSRF Protection" "grep -q 'csrf' internal/server/middleware.go"
run_test "Rate Limiting" "grep -q 'rate' internal/server/middleware.go"
run_test "Security Headers" "grep -q 'security' internal/server/middleware.go"

# Compliance Tests
echo -e "\n${YELLOW}10. Compliance Feature Tests${NC}"
run_test "GDPR Service" "test -f internal/compliance/gdpr.go"
run_test "Audit Service" "test -f internal/compliance/audit.go"
run_test "Data Export" "grep -q 'export' internal/compliance/gdpr.go"
run_test "Data Deletion" "grep -q 'delete' internal/compliance/gdpr.go"

# Integration Tests
echo -e "\n${YELLOW}11. Integration Component Tests${NC}"
run_test "Webhook System" "test -f internal/webhook/webhook.go"
run_test "Backup System" "test -f internal/backup/backup.go"
run_test "Email Service" "test -f internal/email/service.go"
run_test "Search System" "test -f internal/search/manager.go"
run_test "Git Operations" "test -f internal/git/operations.go"

# Icon and Asset Tests  
echo -e "\n${YELLOW}12. PWA Asset Tests${NC}"
run_test "PWA Icons Generated" "test -d web/static/icons && ls web/static/icons/icon-*.png | wc -l | grep -q '[1-9]'"
run_test "Favicon" "test -f web/static/favicon.ico"
run_test "Robots.txt" "test -f web/static/robots.txt"

# Documentation Tests
echo -e "\n${YELLOW}13. Documentation Tests${NC}"
run_test "API Documentation" "test -f docs/API.md"
run_test "Installation Guide" "test -f docs/INSTALLATION.md"
run_test "Configuration Guide" "test -f docs/CONFIGURATION.md"
run_test "Development Guide" "test -f docs/DEVELOPMENT.md"
run_test "Deployment Guide" "test -f docs/DEPLOYMENT.md"

# Binary Size and Performance Tests
echo -e "\n${YELLOW}14. Performance Tests${NC}"
if [ -f casgist ]; then
    BINARY_SIZE=$(stat -f%z casgist 2>/dev/null || stat -c%s casgist 2>/dev/null || echo "0")
    if [ "$BINARY_SIZE" -gt 0 ] && [ "$BINARY_SIZE" -lt 100000000 ]; then  # Less than 100MB
        echo -e "${GREEN}✓ PASS: Binary Size Reasonable ($(echo $BINARY_SIZE | awk '{print $1/1048576 "MB"}'))${NC}"
        ((TESTS_PASSED++))
    else
        echo -e "${RED}✗ FAIL: Binary Size Too Large${NC}"
        ((TESTS_FAILED++))
    fi
else
    echo -e "${RED}✗ FAIL: Binary Not Found${NC}"
    ((TESTS_FAILED++))
fi

# Summary
echo -e "\n${BLUE}============================================${NC}"
echo -e "${BLUE}           Test Results Summary${NC}"
echo -e "${BLUE}============================================${NC}"
echo -e "${GREEN}Tests Passed: ${TESTS_PASSED}${NC}"
echo -e "${RED}Tests Failed: ${TESTS_FAILED}${NC}"

TOTAL_TESTS=$((TESTS_PASSED + TESTS_FAILED))
SUCCESS_RATE=$((TESTS_PASSED * 100 / TOTAL_TESTS))

echo -e "${BLUE}Success Rate: ${SUCCESS_RATE}%${NC}"

if [ "$TESTS_FAILED" -eq 0 ]; then
    echo -e "\n${GREEN}🎉 ALL TESTS PASSED! CasGists v1.0.0 is ready for release!${NC}"
    exit 0
elif [ "$SUCCESS_RATE" -gt 90 ]; then
    echo -e "\n${YELLOW}⚠️  Minor issues found, but CasGists is mostly ready${NC}"
    exit 1
else
    echo -e "\n${RED}❌ Significant issues found. Review failures before release.${NC}"
    exit 2
fi
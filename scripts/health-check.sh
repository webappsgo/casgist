#!/bin/bash

# CasGists Health Check Script
# Comprehensive health monitoring for production deployments

set -euo pipefail

# Configuration
BASE_URL="${CASGISTS_URL:-http://localhost:64080}"
DB_PATH="/var/lib/casgist/casgist.db"
LOG_FILE="/var/log/casgist/health-check.log"
WEBHOOK_URL="${SLACK_WEBHOOK_URL:-}"
EMAIL_TO="${ALERT_EMAIL:-}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Health status
HEALTH_STATUS="HEALTHY"
ISSUES=()

# Functions
log() {
    local msg="[$(date +'%Y-%m-%d %H:%M:%S')] $1"
    echo "$msg" | tee -a "$LOG_FILE"
}

check_pass() {
    echo -e "${GREEN}✓${NC} $1"
}

check_warn() {
    echo -e "${YELLOW}⚠${NC} $1"
    ISSUES+=("WARNING: $1")
    if [[ "$HEALTH_STATUS" == "HEALTHY" ]]; then
        HEALTH_STATUS="WARNING"
    fi
}

check_fail() {
    echo -e "${RED}✗${NC} $1"
    ISSUES+=("ERROR: $1")
    HEALTH_STATUS="CRITICAL"
}

# Send alert
send_alert() {
    local severity="$1"
    local message="$2"
    
    # Send to Slack if webhook URL is configured
    if [[ -n "$WEBHOOK_URL" ]]; then
        curl -s -X POST "$WEBHOOK_URL" \
            -H 'Content-Type: application/json' \
            -d "{
                \"text\": \"CasGists Health Alert\",
                \"attachments\": [{
                    \"color\": \"$([ \"$severity\" = \"CRITICAL\" ] && echo \"danger\" || echo \"warning\")\",
                    \"fields\": [{
                        \"title\": \"Severity\",
                        \"value\": \"$severity\",
                        \"short\": true
                    }, {
                        \"title\": \"Status\",
                        \"value\": \"$HEALTH_STATUS\",
                        \"short\": true
                    }, {
                        \"title\": \"Message\",
                        \"value\": \"$message\"
                    }]
                }]
            }" >/dev/null 2>&1
    fi
    
    # Send email if configured
    if [[ -n "$EMAIL_TO" ]] && command -v mail &> /dev/null; then
        echo -e "Subject: CasGists Health Alert - $severity\n\n$message" | mail -s "CasGists Health Alert" "$EMAIL_TO"
    fi
}

# Health Checks

echo "=== CasGists Health Check ==="
echo "Time: $(date)"
echo "Target: $BASE_URL"
echo ""

# 1. Service Status Check
echo "Checking service status..."
if systemctl is-active --quiet casgist; then
    check_pass "Service is running"
    
    # Get memory usage
    PID=$(systemctl show -p MainPID --value casgist)
    if [[ -n "$PID" ]] && [[ "$PID" != "0" ]]; then
        MEM_RSS=$(ps -p "$PID" -o rss= 2>/dev/null | xargs)
        if [[ -n "$MEM_RSS" ]]; then
            MEM_MB=$((MEM_RSS / 1024))
            if [[ $MEM_MB -gt 1024 ]]; then
                check_warn "High memory usage: ${MEM_MB}MB"
            else
                check_pass "Memory usage: ${MEM_MB}MB"
            fi
        fi
    fi
else
    check_fail "Service is not running"
fi

# 2. HTTP Endpoint Check
echo -e "\nChecking HTTP endpoints..."

# Health endpoint
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/health" || echo "000")
RESPONSE_TIME=$(curl -s -o /dev/null -w "%{time_total}" "$BASE_URL/health" || echo "0")

if [[ "$HTTP_STATUS" == "200" ]]; then
    check_pass "Health endpoint responding (${RESPONSE_TIME}s)"
    
    # Parse health response
    HEALTH_JSON=$(curl -s "$BASE_URL/health")
    if [[ -n "$HEALTH_JSON" ]]; then
        # Check component statuses
        DB_STATUS=$(echo "$HEALTH_JSON" | grep -o '"database":"[^"]*"' | cut -d'"' -f4 || echo "unknown")
        if [[ "$DB_STATUS" != "healthy" ]]; then
            check_warn "Database component status: $DB_STATUS"
        fi
    fi
else
    check_fail "Health endpoint returned $HTTP_STATUS"
fi

# Home page
HOME_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/" || echo "000")
if [[ "$HOME_STATUS" == "200" ]]; then
    check_pass "Home page accessible"
else
    check_warn "Home page returned $HOME_STATUS"
fi

# API endpoint
API_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/health" || echo "000")
if [[ "$API_STATUS" == "200" ]]; then
    check_pass "API endpoint accessible"
else
    check_warn "API endpoint returned $API_STATUS"
fi

# 3. Database Check
echo -e "\nChecking database..."
if [[ -f "$DB_PATH" ]]; then
    check_pass "Database file exists"
    
    # Check size
    DB_SIZE=$(du -sh "$DB_PATH" | cut -f1)
    check_pass "Database size: $DB_SIZE"
    
    # Check integrity
    if sqlite3 "$DB_PATH" "PRAGMA integrity_check;" 2>/dev/null | grep -q "ok"; then
        check_pass "Database integrity check passed"
    else
        check_fail "Database integrity check failed"
    fi
    
    # Check table count
    TABLE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM sqlite_master WHERE type='table';" 2>/dev/null || echo "0")
    if [[ $TABLE_COUNT -gt 0 ]]; then
        check_pass "Database has $TABLE_COUNT tables"
    else
        check_fail "Database has no tables"
    fi
    
    # Check for locks
    LOCK_COUNT=$(lsof "$DB_PATH" 2>/dev/null | wc -l || echo "0")
    if [[ $LOCK_COUNT -gt 10 ]]; then
        check_warn "High number of database locks: $LOCK_COUNT"
    fi
else
    check_fail "Database file not found"
fi

# 4. Disk Space Check
echo -e "\nChecking disk space..."
DISK_USAGE=$(df -h /var/lib/casgist | awk 'NR==2 {print $5}' | sed 's/%//')
DISK_FREE=$(df -h /var/lib/casgist | awk 'NR==2 {print $4}')

if [[ $DISK_USAGE -gt 90 ]]; then
    check_fail "Critical disk usage: ${DISK_USAGE}% (${DISK_FREE} free)"
elif [[ $DISK_USAGE -gt 80 ]]; then
    check_warn "High disk usage: ${DISK_USAGE}% (${DISK_FREE} free)"
else
    check_pass "Disk usage: ${DISK_USAGE}% (${DISK_FREE} free)"
fi

# 5. Log File Check
echo -e "\nChecking logs..."
if [[ -d "/var/log/casgist" ]]; then
    # Check for recent errors
    ERROR_COUNT=$(grep -i "error\|panic\|fatal" /var/log/casgist/casgist.log 2>/dev/null | grep -c "$(date +'%Y-%m-%d')" || echo "0")
    if [[ $ERROR_COUNT -gt 50 ]]; then
        check_fail "High error count in logs: $ERROR_COUNT today"
    elif [[ $ERROR_COUNT -gt 10 ]]; then
        check_warn "Elevated error count in logs: $ERROR_COUNT today"
    else
        check_pass "Error count in logs: $ERROR_COUNT today"
    fi
    
    # Check log size
    LOG_SIZE=$(du -sh /var/log/casgist/ | cut -f1)
    check_pass "Log directory size: $LOG_SIZE"
else
    check_warn "Log directory not found"
fi

# 6. Process Check
echo -e "\nChecking processes..."
PROCESS_COUNT=$(pgrep -c casgist || echo "0")
if [[ $PROCESS_COUNT -eq 0 ]]; then
    check_fail "No CasGists processes found"
elif [[ $PROCESS_COUNT -gt 10 ]]; then
    check_warn "High process count: $PROCESS_COUNT"
else
    check_pass "Process count: $PROCESS_COUNT"
fi

# Check for zombie processes
ZOMBIE_COUNT=$(ps aux | grep -c "[Zz]ombie\|<defunct>" || echo "0")
if [[ $ZOMBIE_COUNT -gt 0 ]]; then
    check_warn "Zombie processes detected: $ZOMBIE_COUNT"
fi

# 7. Connection Check
echo -e "\nChecking connections..."
if command -v ss &> /dev/null; then
    CONN_COUNT=$(ss -tn | grep -c ":64080" || echo "0")
    if [[ $CONN_COUNT -gt 1000 ]]; then
        check_warn "High connection count: $CONN_COUNT"
    else
        check_pass "Active connections: $CONN_COUNT"
    fi
fi

# 8. Backup Check
echo -e "\nChecking backups..."
BACKUP_DIR="/var/lib/casgist/backups"
if [[ -d "$BACKUP_DIR" ]]; then
    LATEST_BACKUP=$(find "$BACKUP_DIR" -name "*.tar.gz" -type f -printf '%T@ %p\n' 2>/dev/null | sort -n | tail -1 | cut -d' ' -f2-)
    if [[ -n "$LATEST_BACKUP" ]]; then
        BACKUP_AGE=$((($(date +%s) - $(stat -c %Y "$LATEST_BACKUP")) / 3600))
        if [[ $BACKUP_AGE -gt 48 ]]; then
            check_warn "Latest backup is ${BACKUP_AGE} hours old"
        else
            check_pass "Latest backup is ${BACKUP_AGE} hours old"
        fi
    else
        check_fail "No backups found"
    fi
else
    check_warn "Backup directory not found"
fi

# 9. Performance Metrics
echo -e "\nChecking performance..."

# CPU usage
CPU_USAGE=$(top -bn1 | grep "casgist" | head -1 | awk '{print $9}' || echo "0")
if [[ -n "$CPU_USAGE" ]] && (( $(echo "$CPU_USAGE > 80" | bc -l) )); then
    check_warn "High CPU usage: ${CPU_USAGE}%"
elif [[ -n "$CPU_USAGE" ]]; then
    check_pass "CPU usage: ${CPU_USAGE}%"
fi

# Response time test
START_TIME=$(date +%s%N)
curl -s "$BASE_URL/api/v1/gists?page=1&per_page=10" >/dev/null 2>&1
END_TIME=$(date +%s%N)
RESPONSE_MS=$(((END_TIME - START_TIME) / 1000000))

if [[ $RESPONSE_MS -gt 1000 ]]; then
    check_warn "Slow API response: ${RESPONSE_MS}ms"
else
    check_pass "API response time: ${RESPONSE_MS}ms"
fi

# Summary
echo -e "\n=== Health Check Summary ==="
echo "Overall Status: $HEALTH_STATUS"

if [[ ${#ISSUES[@]} -gt 0 ]]; then
    echo -e "\nIssues Found:"
    for issue in "${ISSUES[@]}"; do
        echo "  - $issue"
    done
    
    # Send alert if critical
    if [[ "$HEALTH_STATUS" == "CRITICAL" ]]; then
        send_alert "CRITICAL" "Health check failed with ${#ISSUES[@]} issues"
    fi
else
    echo "No issues found - system is healthy!"
fi

# Log summary
log "Health check completed: Status=$HEALTH_STATUS, Issues=${#ISSUES[@]}"

# Exit with appropriate code
case "$HEALTH_STATUS" in
    "HEALTHY")
        exit 0
        ;;
    "WARNING")
        exit 1
        ;;
    "CRITICAL")
        exit 2
        ;;
esac
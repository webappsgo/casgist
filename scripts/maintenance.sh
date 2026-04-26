#!/bin/bash

# CasGists Maintenance Script
# Perform routine maintenance tasks

set -euo pipefail

# Configuration
DATA_DIR="/var/lib/casgist"
LOG_DIR="/var/log/casgist"
BACKUP_DIR="/var/lib/casgist/backups"
TEMP_DIR="/var/lib/casgist/temp"
SERVICE_NAME="casgist"
MAINTENANCE_LOG="/var/log/casgist/maintenance.log"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Functions
log() {
    local msg="[$(date +'%Y-%m-%d %H:%M:%S')] $1"
    echo -e "${GREEN}${msg}${NC}"
    echo "$msg" >> "$MAINTENANCE_LOG"
}

error() {
    local msg="[$(date +'%Y-%m-%d %H:%M:%S')] ERROR: $1"
    echo -e "${RED}${msg}${NC}" >&2
    echo "$msg" >> "$MAINTENANCE_LOG"
    exit 1
}

warn() {
    local msg="[$(date +'%Y-%m-%d %H:%M:%S')] WARNING: $1"
    echo -e "${YELLOW}${msg}${NC}"
    echo "$msg" >> "$MAINTENANCE_LOG"
}

info() {
    echo -e "${BLUE}$1${NC}"
}

# Check if maintenance mode file exists
is_maintenance_mode() {
    [[ -f "$DATA_DIR/.maintenance" ]]
}

# Enable maintenance mode
enable_maintenance() {
    log "Enabling maintenance mode..."
    echo "$(date): Maintenance started by $USER" > "$DATA_DIR/.maintenance"
    
    # Create maintenance page
    cat > "/var/www/html/maintenance.html" <<'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>CasGists - Maintenance Mode</title>
    <style>
        body { font-family: sans-serif; background: #f0f0f0; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
        .container { text-align: center; background: white; padding: 40px; border-radius: 10px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        h1 { color: #333; }
        p { color: #666; }
        .icon { font-size: 48px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="icon">🔧</div>
        <h1>CasGists is Under Maintenance</h1>
        <p>We're performing scheduled maintenance to improve your experience.</p>
        <p>We'll be back shortly. Thank you for your patience!</p>
    </div>
</body>
</html>
EOF
}

# Disable maintenance mode
disable_maintenance() {
    log "Disabling maintenance mode..."
    rm -f "$DATA_DIR/.maintenance"
    rm -f "/var/www/html/maintenance.html"
}

# Clean temporary files
clean_temp_files() {
    log "Cleaning temporary files..."
    
    # Clean temp directory
    if [[ -d "$TEMP_DIR" ]]; then
        find "$TEMP_DIR" -type f -mtime +7 -delete
        find "$TEMP_DIR" -type d -empty -delete
    fi
    
    # Clean orphaned session files
    find "$DATA_DIR" -name "sess_*" -mtime +30 -delete 2>/dev/null || true
    
    # Clean old logs
    find "$LOG_DIR" -name "*.log.gz" -mtime +30 -delete
}

# Clean git repositories
clean_git_repos() {
    log "Cleaning git repositories..."
    
    local repos_dir="$DATA_DIR/repos"
    if [[ -d "$repos_dir" ]]; then
        # Find all git repositories
        find "$repos_dir" -name ".git" -type d | while read -r git_dir; do
            local repo_dir=$(dirname "$git_dir")
            (
                cd "$repo_dir"
                # Run git gc
                git gc --quiet --auto
                # Prune old objects
                git prune --expire=2.weeks.ago
                # Clean reflog
                git reflog expire --expire=30.days --all
            ) 2>/dev/null || warn "Failed to clean repository: $repo_dir"
        done
    fi
}

# Optimize database
optimize_database() {
    log "Optimizing database..."
    
    local db_path="$DATA_DIR/casgist.db"
    if [[ -f "$db_path" ]]; then
        # Backup before optimization
        cp "$db_path" "${db_path}.backup-$(date +%Y%m%d-%H%M%S)"
        
        sqlite3 "$db_path" <<EOF
-- Clean up soft-deleted records older than 30 days
DELETE FROM gists WHERE deleted_at IS NOT NULL AND deleted_at < datetime('now', '-30 days');
DELETE FROM users WHERE deleted_at IS NOT NULL AND deleted_at < datetime('now', '-30 days');

-- Clean old sessions
DELETE FROM sessions WHERE expires_at < datetime('now');

-- Clean old email queue entries
DELETE FROM email_queue WHERE status = 'sent' AND created_at < datetime('now', '-7 days');
DELETE FROM email_queue WHERE status = 'failed' AND created_at < datetime('now', '-30 days');

-- Vacuum and analyze
VACUUM;
ANALYZE;
EOF
        
        log "Database optimization complete"
    else
        warn "Database not found"
    fi
}

# Check and fix permissions
fix_permissions() {
    log "Fixing file permissions..."
    
    # Set ownership
    chown -R casgist:casgist "$DATA_DIR"
    chown -R casgist:casgist "$LOG_DIR"
    
    # Set directory permissions
    find "$DATA_DIR" -type d -exec chmod 750 {} \;
    find "$LOG_DIR" -type d -exec chmod 750 {} \;
    
    # Set file permissions
    find "$DATA_DIR" -type f -name "*.db" -exec chmod 640 {} \;
    find "$DATA_DIR" -type f -name "*.log" -exec chmod 640 {} \;
    
    # Special permissions for repos
    if [[ -d "$DATA_DIR/repos" ]]; then
        find "$DATA_DIR/repos" -type f -name "*.pack" -exec chmod 640 {} \;
    fi
}

# Rotate logs
rotate_logs() {
    log "Rotating logs..."
    
    # Force logrotate
    logrotate -f /etc/logrotate.d/casgist || warn "Logrotate failed"
    
    # Compress old logs
    find "$LOG_DIR" -name "*.log.*" ! -name "*.gz" -mtime +1 -exec gzip {} \;
}

# Update statistics
update_statistics() {
    log "Updating statistics..."
    
    local db_path="$DATA_DIR/casgist.db"
    if [[ -f "$db_path" ]]; then
        # Update system statistics
        sqlite3 "$db_path" <<EOF
-- Update user statistics
UPDATE users SET 
    gist_count = (SELECT COUNT(*) FROM gists WHERE user_id = users.id AND deleted_at IS NULL),
    star_count = (SELECT COUNT(*) FROM stars s JOIN gists g ON s.gist_id = g.id WHERE g.user_id = users.id),
    updated_at = datetime('now');

-- Update gist statistics  
UPDATE gists SET
    star_count = (SELECT COUNT(*) FROM stars WHERE gist_id = gists.id),
    fork_count = (SELECT COUNT(*) FROM gists f WHERE f.forked_from_id = gists.id),
    updated_at = datetime('now')
WHERE deleted_at IS NULL;
EOF
    fi
}

# Backup before maintenance
create_maintenance_backup() {
    log "Creating pre-maintenance backup..."
    /usr/local/bin/casgist-backup || warn "Backup failed"
}

# Health check
run_health_check() {
    log "Running health check..."
    /root/Projects/local/casapps/casgist/scripts/health-check.sh || warn "Health check reported issues"
}

# Main maintenance routine
run_maintenance() {
    local mode="${1:-full}"
    
    info "=== CasGists Maintenance ==="
    info "Mode: $mode"
    info "Time: $(date)"
    echo ""
    
    # Enable maintenance mode
    enable_maintenance
    
    # Stop service for full maintenance
    if [[ "$mode" == "full" ]]; then
        log "Stopping CasGists service..."
        systemctl stop "$SERVICE_NAME"
        sleep 5
    fi
    
    # Create backup
    create_maintenance_backup
    
    # Perform maintenance tasks
    case "$mode" in
        full)
            clean_temp_files
            clean_git_repos
            optimize_database
            fix_permissions
            rotate_logs
            update_statistics
            ;;
        quick)
            clean_temp_files
            rotate_logs
            ;;
        database)
            optimize_database
            update_statistics
            ;;
        *)
            error "Unknown maintenance mode: $mode"
            ;;
    esac
    
    # Start service if it was stopped
    if [[ "$mode" == "full" ]] && ! systemctl is-active --quiet "$SERVICE_NAME"; then
        log "Starting CasGists service..."
        systemctl start "$SERVICE_NAME"
        sleep 5
    fi
    
    # Disable maintenance mode
    disable_maintenance
    
    # Run health check
    run_health_check
    
    log "Maintenance completed successfully!"
}

# Show maintenance status
show_status() {
    info "=== CasGists Maintenance Status ==="
    echo ""
    
    if is_maintenance_mode; then
        warn "Maintenance mode is ACTIVE"
        cat "$DATA_DIR/.maintenance"
    else
        info "Maintenance mode is not active"
    fi
    echo ""
    
    info "Last maintenance log entries:"
    tail -10 "$MAINTENANCE_LOG" 2>/dev/null || echo "No maintenance log found"
    echo ""
    
    info "Scheduled maintenance:"
    crontab -l 2>/dev/null | grep -i maintenance || echo "No scheduled maintenance found"
}

# Schedule maintenance
schedule_maintenance() {
    local schedule="${1:-weekly}"
    
    log "Scheduling $schedule maintenance..."
    
    case "$schedule" in
        daily)
            (crontab -l 2>/dev/null | grep -v "maintenance.sh"; echo "0 3 * * * /root/Projects/local/casapps/casgist/scripts/maintenance.sh run quick >/dev/null 2>&1") | crontab -
            ;;
        weekly)
            (crontab -l 2>/dev/null | grep -v "maintenance.sh"; echo "0 2 * * 0 /root/Projects/local/casapps/casgist/scripts/maintenance.sh run full >/dev/null 2>&1") | crontab -
            ;;
        none)
            crontab -l 2>/dev/null | grep -v "maintenance.sh" | crontab -
            ;;
        *)
            error "Unknown schedule: $schedule (use daily, weekly, or none)"
            ;;
    esac
    
    info "Maintenance schedule updated"
}

# Parse command line arguments
case "${1:-help}" in
    run)
        run_maintenance "${2:-full}"
        ;;
    enable)
        enable_maintenance
        info "Maintenance mode enabled"
        ;;
    disable)
        disable_maintenance
        info "Maintenance mode disabled"
        ;;
    status)
        show_status
        ;;
    schedule)
        schedule_maintenance "${2:-weekly}"
        ;;
    *)
        echo "Usage: $0 {run|enable|disable|status|schedule} [options]"
        echo ""
        echo "Commands:"
        echo "  run [full|quick|database]  - Run maintenance tasks"
        echo "  enable                     - Enable maintenance mode"
        echo "  disable                    - Disable maintenance mode"
        echo "  status                     - Show maintenance status"
        echo "  schedule [daily|weekly|none] - Schedule automatic maintenance"
        echo ""
        echo "Maintenance modes:"
        echo "  full     - Complete maintenance (requires service restart)"
        echo "  quick    - Quick cleanup without service restart"
        echo "  database - Database optimization only"
        exit 1
        ;;
esac
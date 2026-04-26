#!/bin/bash

# CasGists Production Optimization Script
# This script optimizes CasGists for production performance

set -euo pipefail

# Configuration
DATA_DIR="/var/lib/casgist"
CONFIG_DIR="/etc/casgist"
LOG_DIR="/var/log/casgist"
SERVICE_NAME="casgist"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Functions
log() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
    exit 1
}

warn() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Check if running as root
check_root() {
    if [[ $EUID -ne 0 ]]; then
        error "This script must be run as root"
    fi
}

# Optimize SQLite database
optimize_sqlite() {
    local db_path="$DATA_DIR/casgist.db"
    
    if [[ ! -f "$db_path" ]]; then
        warn "SQLite database not found at $db_path"
        return
    fi
    
    log "Optimizing SQLite database..."
    
    # Create backup first
    cp "$db_path" "${db_path}.backup-$(date +%Y%m%d-%H%M%S)"
    
    # Run SQLite optimizations
    sqlite3 "$db_path" <<EOF
-- Vacuum to reclaim space
VACUUM;

-- Analyze to update statistics
ANALYZE;

-- Set optimal pragmas for production
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -64000; -- 64MB cache
PRAGMA temp_store = MEMORY;
PRAGMA mmap_size = 268435456; -- 256MB mmap
PRAGMA page_size = 4096;
PRAGMA auto_vacuum = INCREMENTAL;
PRAGMA wal_autocheckpoint = 1000;

-- Create indexes if they don't exist
CREATE INDEX IF NOT EXISTS idx_gists_user_id_created_at ON gists(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_gists_visibility_created_at ON gists(visibility, created_at);
CREATE INDEX IF NOT EXISTS idx_gist_files_gist_id ON gist_files(gist_id);
CREATE INDEX IF NOT EXISTS idx_stars_gist_id ON stars(gist_id);
CREATE INDEX IF NOT EXISTS idx_stars_user_id ON stars(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
CREATE INDEX IF NOT EXISTS idx_api_tokens_token ON api_tokens(token);
CREATE INDEX IF NOT EXISTS idx_email_queue_status ON email_queue(status, created_at);

-- Update statistics
ANALYZE;
EOF
    
    log "SQLite optimization complete"
}

# Configure system limits
configure_limits() {
    log "Configuring system limits..."
    
    # Create limits file for casgist service
    cat > "/etc/security/limits.d/99-casgist.conf" <<EOF
# CasGists Production Limits
casgist soft nofile 65536
casgist hard nofile 65536
casgist soft nproc 4096
casgist hard nproc 4096
EOF
    
    # Update systemd service limits
    mkdir -p "/etc/systemd/system/${SERVICE_NAME}.service.d"
    cat > "/etc/systemd/system/${SERVICE_NAME}.service.d/limits.conf" <<EOF
[Service]
# File descriptor limits
LimitNOFILE=65536

# Process limits
LimitNPROC=4096

# Memory limits (adjust based on available RAM)
MemoryMax=2G
MemoryHigh=1.5G

# CPU limits
CPUQuota=200%

# IO limits
IOWeight=100
EOF
    
    systemctl daemon-reload
}

# Optimize kernel parameters
optimize_kernel() {
    log "Optimizing kernel parameters..."
    
    # Backup current sysctl settings
    cp /etc/sysctl.conf "/etc/sysctl.conf.backup-$(date +%Y%m%d-%H%M%S)"
    
    # Add CasGists optimizations
    cat >> /etc/sysctl.conf <<EOF

# CasGists Production Optimizations
# Network performance
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 8192
net.core.netdev_max_backlog = 5000
net.ipv4.tcp_congestion_control = bbr
net.core.default_qdisc = fq
net.ipv4.tcp_fastopen = 3

# File system
fs.file-max = 2097152
fs.inotify.max_user_watches = 524288

# Virtual memory
vm.swappiness = 10
vm.dirty_ratio = 15
vm.dirty_background_ratio = 5

# Security
kernel.randomize_va_space = 2
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
EOF
    
    # Apply settings
    sysctl -p
}

# Configure log compression
configure_logs() {
    log "Configuring log management..."
    
    # Update logrotate configuration
    cat > "/etc/logrotate.d/casgist" <<EOF
$LOG_DIR/*.log {
    daily
    missingok
    rotate 14
    compress
    delaycompress
    notifempty
    create 0640 casgist casgist
    sharedscripts
    postrotate
        systemctl reload $SERVICE_NAME >/dev/null 2>&1 || true
    endscript
}
EOF
    
    # Create log cleanup cron job
    cat > "/etc/cron.daily/casgist-cleanup" <<'EOF'
#!/bin/bash
# Clean up old CasGists logs and temporary files

# Remove logs older than 30 days
find /var/log/casgist -name "*.log.gz" -mtime +30 -delete

# Clean temporary files older than 7 days
find /var/lib/casgist/temp -type f -mtime +7 -delete

# Clean orphaned git objects
find /var/lib/casgist/repos -name "*.pack" -mtime +30 -exec git repack -d {} \;
EOF
    
    chmod +x /etc/cron.daily/casgist-cleanup
}

# Configure monitoring
setup_monitoring() {
    log "Setting up monitoring..."
    
    # Create monitoring script
    cat > "/usr/local/bin/casgist-monitor" <<'EOF'
#!/bin/bash

# CasGists Monitoring Script
SERVICE="casgist"
DB_PATH="/var/lib/casgist/casgist.db"
URL="http://localhost:64080/health"
EMAIL_ALERT="admin@localhost"

# Check service status
if ! systemctl is-active --quiet $SERVICE; then
    echo "CasGists service is down" | mail -s "CasGists Alert: Service Down" $EMAIL_ALERT
    systemctl start $SERVICE
fi

# Check database size
DB_SIZE=$(du -sh $DB_PATH 2>/dev/null | cut -f1)
echo "Database size: $DB_SIZE"

# Check health endpoint
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" $URL)
if [[ "$HTTP_STATUS" != "200" ]]; then
    echo "Health check failed with status $HTTP_STATUS" | mail -s "CasGists Alert: Health Check Failed" $EMAIL_ALERT
fi

# Check disk usage
DISK_USAGE=$(df -h /var/lib/casgist | awk 'NR==2 {print $5}' | sed 's/%//')
if [[ $DISK_USAGE -gt 80 ]]; then
    echo "Disk usage is at ${DISK_USAGE}%" | mail -s "CasGists Alert: High Disk Usage" $EMAIL_ALERT
fi
EOF
    
    chmod +x /usr/local/bin/casgist-monitor
    
    # Add to cron
    (crontab -l 2>/dev/null; echo "*/5 * * * * /usr/local/bin/casgist-monitor >/dev/null 2>&1") | crontab -
}

# Configure backup
setup_backup() {
    log "Setting up automated backups..."
    
    # Create backup script
    cat > "/usr/local/bin/casgist-backup" <<'EOF'
#!/bin/bash

# CasGists Backup Script
BACKUP_DIR="/var/lib/casgist/backups"
DB_PATH="/var/lib/casgist/casgist.db"
REPOS_DIR="/var/lib/casgist/repos"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
BACKUP_NAME="casgist-backup-${TIMESTAMP}"

# Create backup directory
mkdir -p "$BACKUP_DIR"

# Create temporary backup directory
TEMP_DIR=$(mktemp -d)
mkdir -p "$TEMP_DIR/$BACKUP_NAME"

# Backup database
if [[ -f "$DB_PATH" ]]; then
    sqlite3 "$DB_PATH" ".backup '$TEMP_DIR/$BACKUP_NAME/database.db'"
fi

# Backup repositories
if [[ -d "$REPOS_DIR" ]]; then
    tar -czf "$TEMP_DIR/$BACKUP_NAME/repos.tar.gz" -C "$REPOS_DIR" .
fi

# Backup configuration
if [[ -d "/etc/casgist" ]]; then
    tar -czf "$TEMP_DIR/$BACKUP_NAME/config.tar.gz" -C "/etc/casgist" .
fi

# Create final backup archive
cd "$TEMP_DIR"
tar -czf "$BACKUP_DIR/${BACKUP_NAME}.tar.gz" "$BACKUP_NAME"

# Clean up
rm -rf "$TEMP_DIR"

# Remove old backups (keep last 7 days)
find "$BACKUP_DIR" -name "casgist-backup-*.tar.gz" -mtime +7 -delete

echo "Backup completed: $BACKUP_DIR/${BACKUP_NAME}.tar.gz"
EOF
    
    chmod +x /usr/local/bin/casgist-backup
    
    # Add to cron (daily at 2 AM)
    (crontab -l 2>/dev/null; echo "0 2 * * * /usr/local/bin/casgist-backup >/dev/null 2>&1") | crontab -
}

# Configure nginx (if installed)
configure_nginx() {
    if ! command -v nginx &> /dev/null; then
        warn "Nginx not found, skipping nginx configuration"
        return
    fi
    
    log "Configuring nginx for CasGists..."
    
    cat > "/etc/nginx/sites-available/casgist" <<'EOF'
# CasGists Nginx Configuration
upstream casgist {
    server 127.0.0.1:64080;
    keepalive 32;
}

# Rate limiting
limit_req_zone $binary_remote_addr zone=casgist_limit:10m rate=10r/s;
limit_conn_zone $binary_remote_addr zone=casgist_conn:10m;

server {
    listen 80;
    listen [::]:80;
    server_name _;
    
    # Security headers
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
    
    # Rate limiting
    limit_req zone=casgist_limit burst=20 nodelay;
    limit_conn casgist_conn 100;
    
    # Compression
    gzip on;
    gzip_vary on;
    gzip_min_length 1024;
    gzip_types text/plain text/css text/xml text/javascript application/javascript application/json application/xml+rss;
    
    # Client limits
    client_max_body_size 50M;
    client_body_timeout 30s;
    client_header_timeout 10s;
    
    # Proxy to CasGists
    location / {
        proxy_pass http://casgist;
        proxy_http_version 1.1;
        
        # Headers
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # Connection settings
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_request_buffering off;
        
        # Timeouts
        proxy_connect_timeout 10s;
        proxy_send_timeout 30s;
        proxy_read_timeout 30s;
    }
    
    # Static files with caching
    location /static/ {
        proxy_pass http://casgist;
        expires 30d;
        add_header Cache-Control "public, immutable";
    }
    
    # Health check endpoint
    location /health {
        proxy_pass http://casgist;
        access_log off;
    }
}
EOF
    
    # Enable site
    ln -sf /etc/nginx/sites-available/casgist /etc/nginx/sites-enabled/
    
    # Test and reload nginx
    nginx -t && systemctl reload nginx
}

# Main optimization function
optimize() {
    log "Starting CasGists production optimization..."
    
    check_root
    
    # Stop service during optimization
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log "Stopping CasGists service for optimization..."
        systemctl stop "$SERVICE_NAME"
    fi
    
    # Run optimizations
    optimize_sqlite
    configure_limits
    optimize_kernel
    configure_logs
    setup_monitoring
    setup_backup
    configure_nginx
    
    # Start service
    log "Starting CasGists service..."
    systemctl start "$SERVICE_NAME"
    
    # Final status check
    sleep 2
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log "CasGists optimization complete!"
        log "Service is running and optimized for production"
    else
        error "Service failed to start after optimization"
    fi
}

# Show current status
show_status() {
    echo "=== CasGists Production Status ==="
    echo ""
    
    # Service status
    echo "Service Status:"
    systemctl status "$SERVICE_NAME" --no-pager | head -10
    echo ""
    
    # Database info
    if [[ -f "$DATA_DIR/casgist.db" ]]; then
        echo "Database Info:"
        echo -n "  Size: "
        du -sh "$DATA_DIR/casgist.db" | cut -f1
        echo -n "  Tables: "
        sqlite3 "$DATA_DIR/casgist.db" "SELECT COUNT(*) FROM sqlite_master WHERE type='table';" 2>/dev/null || echo "N/A"
        echo ""
    fi
    
    # Disk usage
    echo "Disk Usage:"
    df -h "$DATA_DIR"
    echo ""
    
    # System limits
    echo "System Limits:"
    echo -n "  Open files: "
    ulimit -n
    echo -n "  Processes: "
    ulimit -u
    echo ""
    
    # Recent logs
    echo "Recent Logs:"
    tail -5 "$LOG_DIR/casgist.log" 2>/dev/null || echo "No logs available"
}

# Parse command line arguments
case "${1:-optimize}" in
    optimize)
        optimize
        ;;
    status)
        show_status
        ;;
    backup)
        /usr/local/bin/casgist-backup
        ;;
    monitor)
        /usr/local/bin/casgist-monitor
        ;;
    *)
        echo "Usage: $0 {optimize|status|backup|monitor}"
        echo ""
        echo "Commands:"
        echo "  optimize - Run all production optimizations"
        echo "  status   - Show current system status"
        echo "  backup   - Run manual backup"
        echo "  monitor  - Run monitoring check"
        exit 1
        ;;
esac
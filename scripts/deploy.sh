#!/bin/bash

# CasGists Production Deployment Script
# This script helps deploy CasGists to a production server

set -euo pipefail

# Configuration
INSTALL_DIR="/opt/casgist"
DATA_DIR="/var/lib/casgist"
CONFIG_DIR="/etc/casgist"
LOG_DIR="/var/log/casgist"
SERVICE_NAME="casgist"
SERVICE_USER="casgist"
SERVICE_GROUP="casgist"

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

# Create system user
create_user() {
    if ! id "$SERVICE_USER" &>/dev/null; then
        log "Creating system user: $SERVICE_USER"
        useradd --system --shell /bin/false --home-dir "$DATA_DIR" --create-home "$SERVICE_USER"
    else
        log "User $SERVICE_USER already exists"
    fi
}

# Create directories
create_directories() {
    log "Creating directories..."
    
    directories=(
        "$INSTALL_DIR"
        "$CONFIG_DIR"
        "$LOG_DIR"
        "$DATA_DIR"
        "$DATA_DIR/repos"
        "$DATA_DIR/attachments"
        "$DATA_DIR/backups"
        "$DATA_DIR/temp"
    )
    
    for dir in "${directories[@]}"; do
        mkdir -p "$dir"
        chown "$SERVICE_USER:$SERVICE_GROUP" "$dir"
        
        # Set appropriate permissions
        if [[ "$dir" == "$CONFIG_DIR" ]]; then
            chmod 750 "$dir"
        elif [[ "$dir" == "$DATA_DIR"* ]]; then
            chmod 750 "$dir"
        else
            chmod 755 "$dir"
        fi
    done
}

# Install binary
install_binary() {
    local binary_path="$1"
    
    if [[ ! -f "$binary_path" ]]; then
        error "Binary not found: $binary_path"
    fi
    
    log "Installing binary..."
    cp "$binary_path" "$INSTALL_DIR/casgist"
    chmod 755 "$INSTALL_DIR/casgist"
    chown root:root "$INSTALL_DIR/casgist"
    
    # Create symlink
    ln -sf "$INSTALL_DIR/casgist" /usr/local/bin/casgist
}

# Create config file
create_config() {
    local config_file="$CONFIG_DIR/config.yaml"
    
    if [[ -f "$config_file" ]]; then
        log "Config file already exists, skipping..."
        return
    fi
    
    log "Creating config file..."
    cat > "$config_file" <<EOF
# CasGists Production Configuration

server:
  port: 64080
  host: "0.0.0.0"
  enable_https: false
  # cert_file: /etc/casgist/cert.pem
  # key_file: /etc/casgist/key.pem

database:
  type: sqlite
  dsn: "${DATA_DIR}/casgist.db"
  max_connections: 25
  max_idle_time: 300

paths:
  data_dir: "${DATA_DIR}"
  config_dir: "${CONFIG_DIR}"
  log_dir: "${LOG_DIR}"
  repos_dir: "${DATA_DIR}/repos"
  attachments_dir: "${DATA_DIR}/attachments"
  backups_dir: "${DATA_DIR}/backups"
  temp_dir: "${DATA_DIR}/temp"

log:
  level: info
  format: json
  file: "${LOG_DIR}/casgist.log"
  max_size: 100 # MB
  max_backups: 7
  max_age: 30 # days

security:
  secret_key: "$(openssl rand -hex 32)"
  enable_csrf: true
  enable_cors: false
  # allowed_origins: ["https://example.com"]

email:
  enabled: false
  # smtp_host: smtp.example.com
  # smtp_port: 587
  # smtp_user: noreply@example.com
  # smtp_password: ""
  # from_address: noreply@example.com
  # from_name: CasGists

cache:
  type: memory
  # redis_url: redis://localhost:6379

search:
  backend: sqlite_fts

backup:
  enabled: true
  schedule: "0 2 * * *" # 2 AM daily
  retention: 7 # days

webhook:
  enabled: true
  workers: 5
  timeout: 30s

ratelimit:
  enabled: true
  authenticated: 1000
  anonymous: 100
  login_attempts: 5

production: true
EOF

    chown root:"$SERVICE_GROUP" "$config_file"
    chmod 640 "$config_file"
}

# Create systemd service
create_service() {
    local service_file="/etc/systemd/system/${SERVICE_NAME}.service"
    
    log "Creating systemd service..."
    cat > "$service_file" <<EOF
[Unit]
Description=CasGists - Self-hosted GitHub Gists alternative
Documentation=https://github.com/casapps/casgist
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
ExecStart=$INSTALL_DIR/casgist
Restart=on-failure
RestartSec=10
StandardOutput=append:$LOG_DIR/casgist.log
StandardError=append:$LOG_DIR/casgist.log

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DATA_DIR $LOG_DIR
ReadOnlyPaths=$CONFIG_DIR
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictRealtime=true
RestrictSUIDSGID=true
RemoveIPC=true
PrivateMounts=true

# Resource limits
LimitNOFILE=65536
LimitNPROC=512

# Environment
Environment="CASGISTS_CONFIG_FILE=$CONFIG_DIR/config.yaml"
Environment="CASGISTS_DATA_DIR=$DATA_DIR"
Environment="CASGISTS_LOG_DIR=$LOG_DIR"

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
}

# Setup log rotation
setup_logrotate() {
    log "Setting up log rotation..."
    cat > "/etc/logrotate.d/casgist" <<EOF
$LOG_DIR/*.log {
    daily
    missingok
    rotate 7
    compress
    delaycompress
    notifempty
    create 0640 $SERVICE_USER $SERVICE_GROUP
    sharedscripts
    postrotate
        systemctl reload $SERVICE_NAME >/dev/null 2>&1 || true
    endscript
}
EOF
}

# Setup firewall (ufw)
setup_firewall() {
    if command -v ufw &> /dev/null; then
        log "Configuring firewall..."
        ufw allow 64080/tcp comment 'CasGists' || true
    else
        warn "UFW not found, skipping firewall configuration"
    fi
}

# Main deployment function
deploy() {
    local binary_path="${1:-./build/casgist}"
    
    log "Starting CasGists deployment..."
    
    # Stop service if running
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log "Stopping existing service..."
        systemctl stop "$SERVICE_NAME"
    fi
    
    # Deploy steps
    check_root
    create_user
    create_directories
    install_binary "$binary_path"
    create_config
    create_service
    setup_logrotate
    setup_firewall
    
    # Start service
    log "Starting CasGists service..."
    systemctl enable "$SERVICE_NAME"
    systemctl start "$SERVICE_NAME"
    
    # Check status
    sleep 2
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log "CasGists deployed successfully!"
        log "Service status:"
        systemctl status "$SERVICE_NAME" --no-pager
        log ""
        log "Access CasGists at: http://$(hostname -I | awk '{print $1}'):64080"
    else
        error "Service failed to start. Check logs at: $LOG_DIR/casgist.log"
    fi
}

# Parse command line arguments
case "${1:-deploy}" in
    deploy)
        deploy "${2:-}"
        ;;
    uninstall)
        log "Uninstalling CasGists..."
        systemctl stop "$SERVICE_NAME" || true
        systemctl disable "$SERVICE_NAME" || true
        rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
        rm -f "/etc/logrotate.d/casgist"
        rm -f "/usr/local/bin/casgist"
        systemctl daemon-reload
        log "CasGists uninstalled (data preserved in $DATA_DIR)"
        ;;
    status)
        systemctl status "$SERVICE_NAME"
        ;;
    logs)
        journalctl -u "$SERVICE_NAME" -f
        ;;
    *)
        echo "Usage: $0 {deploy|uninstall|status|logs} [binary-path]"
        exit 1
        ;;
esac
# Configuration

CasGist uses environment variables or config file.

## Environment Variables

```bash
CASGISTS_DB_TYPE=sqlite
CASGISTS_DB_DSN=/data/casgist.db
CASGISTS_DATA_DIR=/var/lib/casgist
CASGISTS_LOG_DIR=/var/log/casgist
```

## Configuration File

Location: `/etc/casapps/casgist/server.yml`

```yaml
server:
  port: 64080
  host: 0.0.0.0

database:
  type: sqlite
  dsn: /data/casgist.db

security:
  enable_2fa: true
  session_timeout: 24h
```

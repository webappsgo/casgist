# API Documentation

## Authentication

```bash
curl -X POST http://localhost:64080/api/v1/auth/login \
  -d '{"email":"user@example.com","password":"secret"}'
```

## Gists

### List Gists

```bash
curl http://localhost:64080/api/v1/gists
```

### Create Gist

```bash
curl -X POST http://localhost:64080/api/v1/gists \
  -H "Authorization: Bearer TOKEN" \
  -d '{
    "title": "My Gist",
    "files": [{"name":"test.txt","content":"Hello"}]
  }'
```

### GitHub Compatibility

CasGist implements GitHub Gist API:

```bash
curl https://api.github.com/gists/ID
```

# Installation

## Docker (Recommended)

```bash
docker run -d \
  --name casgist \
  -p 64080:80 \
  -v casgist_data:/data \
  -v casgist_config:/config \
  ghcr.io/casapps/casgist:latest
```

## Binary

Download from [releases](https://github.com/casapps/casgist/releases).

```bash
# Linux
wget https://github.com/casapps/casgist/releases/latest/download/casgist-linux-amd64
chmod +x casgist-linux-amd64
./casgist-linux-amd64

# macOS
wget https://github.com/casapps/casgist/releases/latest/download/casgist-darwin-amd64
chmod +x casgist-darwin-amd64
./casgist-darwin-amd64
```

## Service Installation

```bash
sudo ./casgist --service --install
sudo systemctl start casgist
```

<div align="center">

# Relay Pulse

### Reject False Positives — Real API-Based LLM Service Quality Monitor

[中文](README.md) | **English**

[![Live Demo](https://img.shields.io/badge/🌐_Live_Demo-relaypulse.top-00d8ff?style=for-the-badge)](https://relaypulse.top)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
[![React](https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react)](https://react.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue?style=for-the-badge)](LICENSE)

<img src="docs/screenshots/dashboard-preview.png" alt="RelayPulse Dashboard" width="100%">

</div>

---

## Introduction

Traditional monitoring tools (like Uptime Kuma) check HTTP connectivity — but in LLM relay scenarios, **"HTTP 200 yet empty or error response"** false positives are common.

**RelayPulse** consumes real tokens to make periodic API requests and validates response content. Only when the LLM actually "speaks" is it considered available.

## ✨ Key Features

- **💸 Real API Probing** - Consumes real tokens, no false positives
- **📊 Visual Matrix** - 24h/7d/30d availability heatmap, see service quality at a glance
- **🔄 Hot Reload** - fsnotify-based config reload, no restart needed
- **💾 Multiple Backends** - SQLite (standalone) / PostgreSQL (K8s)
- **🐳 Cloud Native** - Minimal Docker image, horizontal scaling ready

## 🎯 Use Cases

- Self-hosted/purchased LLM relay services, continuous SLA verification
- Multi-cloud LLM provider comparison, monitor latency and error rates
- External API dependency monitoring, prevent "false positive" outages

## 💰 Cost & Privacy

- **Ultra-low probe cost**: `max_tokens: 1`, ~20 input + 1 output tokens per probe; default once per minute, ~30K tokens/day/service
- **Local data storage**: Config and keys stored locally/self-hosted only, no data sent externally

## 🚀 Quick Start

### Docker Deployment (Recommended)

```bash
# 1. Download config files
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/docker-compose.yaml
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/config.yaml.example

# 2. Prepare config
mkdir -p config && cp config.yaml.example config/config.yaml
vi config/config.yaml  # Add your API Key

# 3. Start service
docker compose up -d

# 4. Open Web UI
open http://localhost:8080
```

**🎬 Full installation guide**: [QUICKSTART.md](QUICKSTART.md)

### Local Development

```bash
# Install dependencies
go mod tidy
cd frontend && npm install && cd ..

# Prepare config
cp config.yaml.example config.yaml

# Start dev server (with hot reload)
make dev

# Or run directly
go run cmd/server/main.go
```

**👨‍💻 Developer guide**: [CONTRIBUTING.md](CONTRIBUTING.md)

## 📖 Documentation

| I want to...              | Read this |
|---------------------------|-----------|
| 🚀 Get running in 5 mins  | [QUICKSTART.md](QUICKSTART.md) |
| 💻 Local dev/debug        | "Local Development" section above |
| ⚙️ Configure monitors     | [Config Guide](docs/user/config.md) |
| 🤝 Contribute             | [CONTRIBUTING.md](CONTRIBUTING.md) |

## 🔧 Configuration Example

```yaml
# config.yaml
interval: "1m"         # Check frequency
slow_latency: "5s"     # Slow request threshold

monitors:
  - provider: "88code"
    service: "cc"
    category: "commercial"
    sponsor: "Team owned"
    sponsor_level: "backbone"  # Optional: public/signal/pulse/beacon/backbone/core (basic/advanced/enterprise are deprecated aliases)
    url: "https://api.88code.com/v1/chat/completions"
    method: "POST"
    api_key: "sk-xxx"  # Or via env var MONITOR_88CODE_CC_API_KEY
    headers:
      Authorization: "Bearer {{API_KEY}}"
    body: |
      {
        "model": "claude-3-opus",
        "messages": [{"role": "user", "content": "hi"}],
        "max_tokens": 1
      }
```

**Detailed config docs**: [docs/user/config.md](docs/user/config.md)

## 🗄️ Storage Backends

| Backend        | Use Case                  | Advantages              |
|----------------|---------------------------|-------------------------|
| **SQLite**     | Standalone, development   | Zero config, works OOTB |
| **PostgreSQL** | K8s, multi-replica        | HA, horizontal scaling  |

```bash
# SQLite (default)
docker compose up -d monitor

# PostgreSQL
docker compose up -d postgres monitor-pg
```

## 📊 API Endpoints

```bash
# Get status (24h)
curl http://localhost:8080/api/status

# Get 7-day history
curl http://localhost:8080/api/status?period=7d

# Health check
curl http://localhost:8080/health

# Version info
curl http://localhost:8080/api/version
```

**Time Window**: The API uses a **sliding window** design. `period=24h` returns data from "24 hours ago until now". This means:
- Each request has a different time baseline, so bucket boundaries shift slightly
- Provider rankings always reflect the **true availability over the last 24 hours**
- For stable data integration, consider sampling at fixed intervals (e.g., every hour on the hour)

## 🛠️ Tech Stack

**Backend**
- Go 1.24+
- Gin (HTTP framework)
- SQLite / PostgreSQL
- fsnotify (hot reload)

**Frontend**
- React 19
- TypeScript
- Tailwind CSS v4
- Vite

## 💎 Sponsorship

Sponsored channels (pinning, pricing tiers, featured placement) are currently offered to the **Chinese-speaking community only** — pricing, QQ-based support, and listing agreements are authored in Simplified Chinese. English-speaking operators are welcome to use RelayPulse as an open-source monitoring tool without sponsorship.

- Tier & rights overview (CN): [docs/user/sponsorship.md](docs/user/sponsorship.md)
- Listing agreement (CN): [docs/user/sponsorship-agreement.md](docs/user/sponsorship-agreement.md)

## 📝 Changelog

See [GitHub Releases](https://github.com/prehisle/relay-pulse/releases) for version history.

## 🤝 Contributing

Issues and Pull Requests welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

## 📈 Star History

[![Star History Chart](https://star-history.dera.page/svg?repos=prehisle/relay-pulse&type=Date)](https://star-history.dera.page/#prehisle/relay-pulse&Date)

## ⚠️ Disclaimer

This project is a technical monitoring tool provided under the MIT License.

**Operational Liability**: The author is not responsible for the content, reliability, credibility, or financial security of any third-party services monitored or listed by instances of this software (including relaypulse.top). Users interact with third-party service providers at their own risk.

## 📄 License

[MIT License](LICENSE) © 2025

---

- **🌐 Live Demo**: https://relaypulse.top
- **📦 Docker Image**: `ghcr.io/prehisle/relay-pulse:latest`
- **💬 Issues**: https://github.com/prehisle/relay-pulse/issues

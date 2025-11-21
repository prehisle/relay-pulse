# 快速部署指南 ⚡

> **一键启动 LLM 服务可用性监控系统**

## 5 分钟快速部署

### 前置要求

- Docker 20.10+
- Docker Compose v2.0+

### 部署步骤

#### 1. 下载配置文件

```bash
# 创建项目目录
mkdir relay-pulse && cd relay-pulse

# 下载 docker-compose.yaml
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/docker-compose.yaml

# 下载配置模板
curl -O https://raw.githubusercontent.com/prehisle/relay-pulse/main/config.yaml.example
```

#### 2. 准备配置文件

```bash
# 复制配置模板
cp config.yaml.example config.yaml

# 编辑配置（填入你的 API Key 和服务端点）
vim config.yaml
```

**最小配置示例**：

```yaml
interval: "1m"
slow_latency: "5s"

monitors:
  - provider: "openai"
    service: "gpt-4"
    url: "https://api.openai.com/v1/chat/completions"
    method: "POST"
    api_key: "sk-your-api-key-here"
    headers:
      Authorization: "Bearer {{API_KEY}}"
      Content-Type: "application/json"
    body: |
      {
        "model": "gpt-4",
        "messages": [{"role": "user", "content": "hello"}],
        "max_tokens": 10
      }
```

#### 3. 一键启动

```bash
docker compose up -d
```

#### 4. 访问服务

- **Web 界面**: http://localhost:8080
- **API 端点**: http://localhost:8080/api/status
- **健康检查**: http://localhost:8080/health

完成！🎉

---

## 常用命令

```bash
# 查看运行状态
docker compose ps

# 查看实时日志
docker compose logs -f monitor

# 停止服务
docker compose down

# 重启服务
docker compose restart

# 更新到最新版本
docker compose pull
docker compose up -d
```

---

## 高级配置

### 使用环境变量（推荐生产环境）

**优点**：API Key 不写在配置文件中，更安全

#### 1. 创建环境变量文件

```bash
cat > .env <<'EOF'
MONITOR_OPENAI_GPT4_API_KEY=sk-your-real-api-key
MONITOR_ANTHROPIC_CLAUDE_API_KEY=sk-ant-your-key
EOF
```

#### 2. 配置文件中使用占位符

```yaml
monitors:
  - provider: "openai"
    service: "gpt-4"
    url: "https://api.openai.com/v1/chat/completions"
    method: "POST"
    # api_key 留空或不填，将从环境变量读取
    headers:
      Authorization: "Bearer {{API_KEY}}"
```

#### 3. 启动时加载环境变量

```bash
docker compose --env-file .env up -d
```

**环境变量命名规则**：

```
MONITOR_<PROVIDER>_<SERVICE>_API_KEY
```

- `<PROVIDER>`: 配置中的 provider 字段（大写，`-` 替换为 `_`）
- `<SERVICE>`: 配置中的 service 字段（大写，`-` 替换为 `_`）

**示例**：

| 配置 | 环境变量名 |
|------|-----------|
| `provider: "88code"`, `service: "cc"` | `MONITOR_88CODE_CC_API_KEY` |
| `provider: "openai"`, `service: "gpt-4"` | `MONITOR_OPENAI_GPT4_API_KEY` |
| `provider: "anthropic"`, `service: "claude-3"` | `MONITOR_ANTHROPIC_CLAUDE3_API_KEY` |

---

## 数据持久化

### SQLite 数据库

数据自动保存在 Docker 命名卷 `relay-pulse-data` 中，重启容器不会丢失。

**查看数据卷**：

```bash
docker volume ls | grep relay-pulse
```

**备份数据库**：

```bash
docker compose exec monitor sh -c 'cp /app/monitor.db /app/data/monitor.db.backup'
docker cp relaypulse-monitor:/app/data/monitor.db.backup ./
```

**恢复数据库**：

```bash
docker cp ./monitor.db.backup relaypulse-monitor:/app/monitor.db
docker compose restart
```

---

## 配置热更新

修改配置文件后，**无需重启容器**，服务会自动检测并重载配置：

```bash
# 1. 编辑配置
vim config.yaml

# 2. 观察日志，等待配置重载提示
docker compose logs -f monitor

# 输出示例：
# [Config] 检测到配置文件变更，正在重载...
# [Config] 热更新成功！已加载 5 个监控任务
```

---

## 监控多个服务示例

```yaml
interval: "1m"
slow_latency: "5s"

monitors:
  # OpenAI GPT-4
  - provider: "openai"
    service: "gpt-4"
    url: "https://api.openai.com/v1/chat/completions"
    method: "POST"
    api_key: "sk-openai-key"
    headers:
      Authorization: "Bearer {{API_KEY}}"
      Content-Type: "application/json"
    body: |
      {"model": "gpt-4", "messages": [{"role": "user", "content": "hi"}], "max_tokens": 5}

  # Anthropic Claude
  - provider: "anthropic"
    service: "claude-3-opus"
    url: "https://api.anthropic.com/v1/messages"
    method: "POST"
    api_key: "sk-ant-key"
    headers:
      x-api-key: "{{API_KEY}}"
      anthropic-version: "2023-06-01"
      Content-Type: "application/json"
    body: |
      {"model": "claude-3-opus-20240229", "messages": [{"role": "user", "content": "hi"}], "max_tokens": 10}

  # Google Gemini
  - provider: "google"
    service: "gemini-pro"
    url: "https://generativelanguage.googleapis.com/v1/models/gemini-pro:generateContent?key={{API_KEY}}"
    method: "POST"
    api_key: "your-google-api-key"
    headers:
      Content-Type: "application/json"
    body: |
      {"contents": [{"parts": [{"text": "hi"}]}]}
```

---

## 自定义端口

默认端口是 `8080`，如需修改：

```bash
# 编辑 docker-compose.yaml
vim docker-compose.yaml

# 修改 ports 部分
ports:
  - "3000:8080"  # 本地 3000 端口映射到容器 8080
```

---

## 故障排查

### 容器无法启动

```bash
# 查看详细日志
docker compose logs monitor

# 检查配置文件语法
docker compose config
```

### 配置文件找不到

确保 `config.yaml` 在 `docker-compose.yaml` 同目录下：

```bash
ls -la config.yaml docker-compose.yaml
```

### 数据库权限问题

```bash
# 检查容器内文件权限
docker compose exec monitor ls -la /app/
```

### 服务无法访问

```bash
# 检查容器状态
docker compose ps

# 检查端口占用
lsof -i :8080

# 测试健康检查
curl http://localhost:8080/health
```

---

## 卸载

```bash
# 停止并删除容器
docker compose down

# 同时删除数据卷（⚠️ 会丢失所有历史数据）
docker compose down -v

# 删除镜像
docker rmi ghcr.io/prehisle/relay-pulse:latest
```

---

## 生产部署建议

### 1. 使用 HTTPS（Nginx 反向代理）

虽然前后端已集成，但生产环境建议用 Nginx 提供 HTTPS：

```nginx
server {
    listen 443 ssl http2;
    server_name monitor.example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### 2. 资源限制

编辑 `docker-compose.yaml`，取消注释资源限制：

```yaml
deploy:
  resources:
    limits:
      cpus: '1'
      memory: 512M
    reservations:
      cpus: '0.5'
      memory: 256M
```

### 3. 日志轮转

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "10m"
    max-file: "3"
```

### 4. 定期备份数据库

```bash
# 添加到 crontab
0 2 * * * docker compose -f /path/to/docker-compose.yaml exec monitor sh -c 'cp /app/monitor.db /app/data/backup-$(date +\%Y\%m\%d).db'
```

---

## 更多文档

- **完整 Docker 指南**: [DOCKER.md](DOCKER.md)
- **开发文档**: [CONTRIBUTING.md](CONTRIBUTING.md)
- **架构说明**: [CLAUDE.md](CLAUDE.md)

---

## 支持

- **GitHub Issues**: https://github.com/prehisle/relay-pulse/issues
- **文档**: https://github.com/prehisle/relay-pulse

**祝监控愉快！** 🚀

# relaypulse.top 生产部署指南

## 目标环境

- **域名**: `relaypulse.top`
- **仓库**: https://github.com/prehisle/relay-pulse.git
- **后端**: Go 服务监听 8080（`cmd/server/main.go`），提供 `/api/status`、`/health`
- **前端**: React + Vite 构建，静态托管
- **数据层**: 默认 SQLite，可切换 PostgreSQL

## 部署架构

```
[客户端]
    ↓ HTTPS
[Nginx/Caddy (relaypulse.top:443)]
    ↓ 静态文件
    /var/www/relaypulse.top/dist/
    ↓ API 反向代理 (/api/*, /health)
[后端服务 (127.0.0.1:8080)]
    ↓
[SQLite/PostgreSQL]
```

## 前置准备

### 1. 必备文件清单

| 文件 | 作用 | 备注 |
|------|------|------|
| `config.production.yaml` | 非敏感配置 | 从 `config.yaml.example` 复制 |
| `deploy/relaypulse.env` | 环境变量（密钥） | **必须加入 .gitignore** |
| `frontend/.env.production` | 前端 API 地址 | 设置为 `https://relaypulse.top` |
| `monitor/` 目录 | SQLite/WAL、日志 | 需持久化挂载 |

### 2. 创建配置文件

```bash
# 复制配置模板
cp config.yaml.example config.production.yaml
cp deploy/relaypulse.env.example deploy/relaypulse.env

# 编辑生产环境变量（添加真实 API Key）
vim deploy/relaypulse.env
```

### 3. 准备数据目录

```bash
mkdir -p monitor
touch monitor/monitor.db monitor/monitor.log
chmod 700 monitor
```

## 部署方式

### 方式一：Docker Compose（推荐）

#### 1. 拉取镜像

```bash
docker pull ghcr.io/prehisle/relay-pulse:latest
```

#### 2. 启动服务

**SQLite 模式**:
```bash
docker compose --env-file deploy/relaypulse.env up -d monitor
```

**PostgreSQL 模式**:
```bash
# 先在 deploy/relaypulse.env 中设置:
# MONITOR_STORAGE_TYPE=postgres
# MONITOR_POSTGRES_HOST=postgres
# MONITOR_POSTGRES_PORT=5432
# MONITOR_POSTGRES_USER=monitor
# MONITOR_POSTGRES_PASSWORD=your_password
# MONITOR_POSTGRES_DATABASE=monitor

docker compose --env-file deploy/relaypulse.env up -d postgres monitor-pg
```

#### 3. 查看日志

```bash
docker compose logs -f monitor        # SQLite 模式
docker compose logs -f monitor-pg     # PostgreSQL 模式
```

#### 4. 验证运行

```bash
curl http://localhost:8080/health
curl http://localhost:8080/api/status
```

### 方式二：Systemd + 二进制

#### 1. 编译后端

```bash
go build -o monitor ./cmd/server
```

#### 2. 部署到服务器

```bash
# 创建部署目录
sudo mkdir -p /opt/relay-pulse/{config,monitor}
sudo useradd -r -s /bin/false monitor

# 复制文件
sudo cp monitor /opt/relay-pulse/
sudo cp config.production.yaml /opt/relay-pulse/config/
sudo chown -R monitor:monitor /opt/relay-pulse
```

#### 3. 创建环境变量文件

```bash
sudo vim /etc/relay-pulse.env
```

内容参考 `deploy/relaypulse.env.example`。

#### 4. 创建 Systemd 单元

创建 `/etc/systemd/system/relay-pulse.service`:

```ini
[Unit]
Description=Relay Pulse Monitor
After=network.target

[Service]
Type=simple
User=monitor
WorkingDirectory=/opt/relay-pulse
EnvironmentFile=/etc/relay-pulse.env
ExecStart=/opt/relay-pulse/monitor -config /opt/relay-pulse/config/config.production.yaml
Restart=always
RestartSec=10
LimitNOFILE=4096

# 安全加固
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/relay-pulse/monitor

[Install]
WantedBy=multi-user.target
```

#### 5. 启动服务

```bash
sudo systemctl daemon-reload
sudo systemctl enable relay-pulse.service
sudo systemctl start relay-pulse.service
sudo systemctl status relay-pulse.service
```

#### 6. 查看日志

```bash
sudo journalctl -u relay-pulse.service -f
```

## 前端部署

### 1. 构建前端

```bash
# 确保 frontend/.env.production 中已设置:
# VITE_API_BASE_URL=https://relaypulse.top
# VITE_USE_MOCK_DATA=false

cd frontend
npm ci
npm run build
```

### 2. 上传到服务器

```bash
# 方式 1: rsync
rsync -av dist/ user@relaypulse.top:/var/www/relaypulse.top/dist/

# 方式 2: scp
scp -r dist/* user@relaypulse.top:/var/www/relaypulse.top/dist/
```

### 3. 配置 Nginx

创建 `/etc/nginx/sites-available/relaypulse.top`:

```nginx
server {
    listen 80;
    listen 443 ssl http2;
    server_name relaypulse.top;

    # SSL 证书配置（使用 certbot）
    ssl_certificate /etc/letsencrypt/live/relaypulse.top/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/relaypulse.top/privkey.pem;

    # 静态文件
    root /var/www/relaypulse.top/dist;
    index index.html;

    # Gzip 压缩
    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml application/xml+rss text/javascript;

    # 静态资源缓存
    location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }

    # API 反向代理
    location /api/ {
        proxy_pass http://127.0.0.1:8080/api/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 禁用缓存
        add_header Cache-Control "no-cache, no-store, must-revalidate";
    }

    # 健康检查
    location /health {
        proxy_pass http://127.0.0.1:8080/health;
        access_log off;
    }

    # SPA 路由支持
    location / {
        try_files $uri $uri/ /index.html;
    }

    # HSTS
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
}

# HTTP 重定向到 HTTPS
server {
    listen 80;
    server_name relaypulse.top;
    return 301 https://$server_name$request_uri;
}
```

### 4. 启用站点并重载 Nginx

```bash
sudo ln -s /etc/nginx/sites-available/relaypulse.top /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 5. 申请 SSL 证书

```bash
sudo certbot --nginx -d relaypulse.top
```

## 环境变量说明

### 后端环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `MONITOR_<PROVIDER>_<SERVICE>_API_KEY` | 各服务商 API 密钥 | `MONITOR_88CODE_CC_API_KEY=sk-xxx` |
| `MONITOR_STORAGE_TYPE` | 存储类型 | `sqlite` 或 `postgres` |
| `MONITOR_SQLITE_PATH` | SQLite 数据库路径 | `monitor/monitor.db` |
| `MONITOR_POSTGRES_HOST` | PostgreSQL 主机 | `localhost` 或 `postgres` |
| `MONITOR_POSTGRES_PORT` | PostgreSQL 端口 | `5432` |
| `MONITOR_POSTGRES_USER` | PostgreSQL 用户 | `monitor` |
| `MONITOR_POSTGRES_PASSWORD` | PostgreSQL 密码 | `your_password` |
| `MONITOR_POSTGRES_DATABASE` | PostgreSQL 数据库名 | `monitor` |
| `MONITOR_POSTGRES_SSLMODE` | PostgreSQL SSL 模式 | `require` 或 `disable` |
| `TZ` | 时区 | `Asia/Shanghai` |
| `MONITOR_CORS_ORIGINS` | 额外允许的 CORS 来源 | 逗号分隔，仅开发环境使用 |

### 前端环境变量

| 变量 | 说明 | 值 |
|------|------|-----|
| `VITE_API_BASE_URL` | API 基础地址 | `https://relaypulse.top` |
| `VITE_USE_MOCK_DATA` | 是否使用模拟数据 | `false` |

## 安全加固

### 1. 密钥管理

- ✅ 所有 API Key 存储在环境变量中
- ✅ `deploy/relaypulse.env` 和 `/etc/relay-pulse.env` 必须加入 `.gitignore`
- ✅ 文件权限设置为 600: `chmod 600 /etc/relay-pulse.env`

### 2. CORS 配置

修改 `internal/api/server.go`，限制跨域来源：

```go
// 替换 cors.Default() 为:
config := cors.Config{
    AllowOrigins:     []string{"https://relaypulse.top"},
    AllowMethods:     []string{"GET", "OPTIONS"},
    AllowHeaders:     []string{"Origin", "Content-Type"},
    ExposeHeaders:    []string{"Content-Length"},
    AllowCredentials: false,
    MaxAge:           12 * time.Hour,
}
r.Use(cors.New(config))
```

### 3. HTTPS/TLS

- ✅ 使用 Let's Encrypt 自动续期证书
- ✅ 启用 HSTS 头
- ✅ 强制 HTTP 重定向到 HTTPS

### 4. PostgreSQL 安全

- ✅ 使用 `sslmode=require` 或 `verify-full`
- ✅ 创建最小权限用户，仅授予必要权限
- ✅ 定期备份数据库

### 5. 日志轮转

创建 `/etc/logrotate.d/relay-pulse`:

```
/opt/relay-pulse/monitor/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0644 monitor monitor
}
```

## 部署验证清单

- [ ] `curl -I https://relaypulse.top/` 返回 200
- [ ] `curl https://relaypulse.top/api/status` 返回 JSON 数据
- [ ] `curl http://relaypulse.top/` 自动重定向到 HTTPS
- [ ] 浏览器访问 `https://relaypulse.top` 显示仪表板
- [ ] 检查 CORS 头：`Access-Control-Allow-Origin: https://relaypulse.top`
- [ ] 后端服务状态正常：`systemctl status relay-pulse` 或 `docker compose ps`
- [ ] 数据库有数据：`sqlite3 monitor/monitor.db 'SELECT COUNT(*) FROM probe_history;'`
- [ ] 配置热更新生效：修改 `config.production.yaml`，观察日志 `[Config] 热更新成功`

## 监控与维护

### 查看运行状态

```bash
# Systemd
sudo systemctl status relay-pulse
sudo journalctl -u relay-pulse -f --since "1 hour ago"

# Docker Compose
docker compose ps
docker compose logs -f monitor --tail=100
```

### 数据备份

**SQLite**:
```bash
# 备份数据库
sqlite3 monitor/monitor.db ".backup monitor.db.backup"

# 定时备份 (crontab)
0 2 * * * cd /opt/relay-pulse && sqlite3 monitor/monitor.db ".backup monitor/backup-$(date +\%Y\%m\%d).db"
```

**PostgreSQL**:
```bash
# 备份
pg_dump -h localhost -U monitor monitor > monitor_backup.sql

# 恢复
psql -h localhost -U monitor monitor < monitor_backup.sql
```

### 配置热更新

```bash
# 修改配置文件
vim config.production.yaml

# 无需重启，配置自动生效（观察日志确认）
# Systemd: journalctl -u relay-pulse -f
# Docker: docker compose logs -f monitor
```

### 更新部署

**Docker 方式**:
```bash
docker pull ghcr.io/prehisle/relay-pulse:latest
docker compose --env-file deploy/relaypulse.env up -d --force-recreate monitor
```

**Systemd 方式**:
```bash
# 编译新版本
go build -o monitor ./cmd/server

# 停止服务
sudo systemctl stop relay-pulse

# 替换二进制
sudo cp monitor /opt/relay-pulse/monitor

# 启动服务
sudo systemctl start relay-pulse
```

## 故障排查

### 后端无法启动

```bash
# 检查配置文件语法
./monitor -config config.production.yaml -validate

# 检查端口占用
sudo netstat -tulpn | grep 8080

# 检查环境变量加载
sudo systemctl show relay-pulse --property=Environment
```

### API 返回 404

```bash
# 检查后端路由
curl http://localhost:8080/health

# 检查 Nginx 配置
sudo nginx -t
sudo tail -f /var/log/nginx/error.log
```

### CORS 错误

```bash
# 检查响应头
curl -I https://relaypulse.top/api/status

# 确认 CORS 配置已更新
grep -A 5 "cors.Config" internal/api/server.go
```

### 数据库连接失败

```bash
# SQLite: 检查文件权限
ls -la monitor/monitor.db

# PostgreSQL: 测试连接
psql -h localhost -U monitor -d monitor
```

## 性能优化

### 1. 启用 HTTP/2

Nginx 配置已包含 `http2` 参数。

### 2. 开启 Gzip 压缩

Nginx 配置已包含 gzip 设置。

### 3. 静态资源 CDN

如需使用 CDN，修改前端构建配置：

```bash
# .env.production
VITE_CDN_URL=https://cdn.example.com
```

### 4. 数据库优化

**SQLite**:
- 已启用 WAL 模式（并发读优化）
- 定期执行 `VACUUM` 清理

**PostgreSQL**:
- 调整连接池大小（`config.postgres.example.yaml`）
- 创建索引：`CREATE INDEX idx_timestamp ON probe_history(timestamp);`

## 相关文档

- [项目 README](../README.md)
- [配置文件说明](../config.yaml.example)
- [PostgreSQL 配置](../config.postgres.example.yaml)
- [贡献指南](../CONTRIBUTING.md)

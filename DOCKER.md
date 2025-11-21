# Docker 一键部署指南

## 快速开始

### 1. 准备配置文件

```bash
# 复制示例配置
cp config.yaml.example config.yaml

# 编辑配置文件
vim config.yaml
```

### 2. 一键启动

```bash
# 构建并启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f

# 停止服务
docker-compose down

# 重启服务
docker-compose restart
```

### 3. 访问服务

- **Web 界面**: http://localhost:8080
- **API 端点**: http://localhost:8080/api/status
- **健康检查**: http://localhost:8080/health

## 架构说明

### 嵌入式部署

前端静态文件已**完全嵌入**到 Go 二进制文件中，无需 Nginx 反向代理：

```
┌─────────────────────────────┐
│   Docker Container (8080)   │
│  ┌───────────────────────┐  │
│  │   Go HTTP Server      │  │
│  │  ┌─────────────────┐  │  │
│  │  │  API Routes     │  │  │
│  │  │  /api/status    │  │  │
│  │  │  /health        │  │  │
│  │  └─────────────────┘  │  │
│  │  ┌─────────────────┐  │  │
│  │  │  Static Files   │  │  │
│  │  │  (Embedded)     │  │  │
│  │  │  /assets/*      │  │  │
│  │  │  /               │  │  │
│  │  └─────────────────┘  │  │
│  └───────────────────────┘  │
└─────────────────────────────┘
```

### 构建流程

```
1. Frontend Builder (Node.js)
   ├─ npm ci
   ├─ npm run build
   └─ 输出: dist/

2. Backend Builder (Go)
   ├─ 复制 dist/ 到 internal/api/frontend/dist
   ├─ go:embed 嵌入静态文件
   └─ 编译生成单个二进制文件

3. Runtime (Alpine)
   └─ 仅包含编译好的二进制文件
```

## 配置说明

### 环境变量

在 `docker-compose.yml` 中配置：

```yaml
environment:
  # 时区
  - TZ=Asia/Shanghai

  # API 密钥（覆盖 config.yaml）
  - MONITOR_88CODE_CC_API_KEY=sk-xxx
  - MONITOR_DUCKCODING_CC_API_KEY=sk-xxx
```

### 数据持久化

- **SQLite 数据库**: 挂载卷 `relay-pulse-data`
- **配置文件**: `./config.yaml` → `/app/config.yaml`
- **数据目录**: `./data` → `/app/data`

## 常用命令

```bash
# 查看运行状态
docker-compose ps

# 查看实时日志
docker-compose logs -f relay-pulse

# 进入容器
docker-compose exec relay-pulse sh

# 重新构建
docker-compose build --no-cache

# 清理并重建
docker-compose down -v
docker-compose up -d --build

# 更新配置（热更新）
vim config.yaml
# 监控服务会自动检测配置变更并重载
```

## 端口映射

默认映射 `8080:8080`，如需修改：

```yaml
ports:
  - "3000:8080"  # 本地 3000 映射到容器 8080
```

## 健康检查

容器自带健康检查：

```bash
# 查看健康状态
docker-compose ps

# 手动健康检查
curl http://localhost:8080/health
```

## 故障排查

### 构建失败

```bash
# 查看构建日志
docker-compose build

# 清理缓存重建
docker-compose build --no-cache
```

### 服务无法访问

```bash
# 检查容器状态
docker-compose ps

# 查看日志
docker-compose logs -f relay-pulse

# 检查端口占用
lsof -i :8080
```

### 配置未生效

```bash
# 确认配置文件挂载
docker-compose exec relay-pulse cat /app/config.yaml

# 重启服务
docker-compose restart
```

## 生产部署建议

1. **使用外部数据卷**：
   ```yaml
   volumes:
     - /data/relay-pulse:/app
   ```

2. **配置日志驱动**：
   ```yaml
   logging:
     driver: "json-file"
     options:
       max-size: "10m"
       max-file: "3"
   ```

3. **资源限制**：
   ```yaml
   deploy:
     resources:
       limits:
         cpus: '0.5'
         memory: 256M
   ```

4. **使用 .env 文件**：
   ```bash
   # .env
   MONITOR_88CODE_CC_API_KEY=sk-xxx
   MONITOR_DUCKCODING_CC_API_KEY=sk-xxx
   ```

## 多环境部署

### 开发环境

```bash
docker-compose -f docker-compose.dev.yml up
```

### 生产环境

```bash
docker-compose -f docker-compose.prod.yml up -d
```

## 技术细节

### Go embed

使用 Go 1.16+ 的 `embed` 包：

```go
//go:embed frontend/dist
var frontendFS embed.FS
```

### 路由策略

- `/api/*` → API 处理器
- `/assets/*` → 静态资源（embed FS）
- `/*` → SPA 回退（index.html）

### 优势

- ✅ 单一二进制文件，部署简单
- ✅ 无需 Nginx，减少组件复杂度
- ✅ 更小的镜像体积
- ✅ 更快的启动速度
- ✅ 完整的 Go 生态工具支持

# Docker 部署故障排查指南

## 常见错误及解决方案

### 1. ContainerConfig KeyError

**错误信息**:
```
KeyError: 'ContainerConfig'
```

**原因**: docker-compose v1 (1.29.2) 与新版 Docker 镜像格式不兼容

**解决方案**:

#### 方案 A: 升级到 Docker Compose V2（推荐）

```bash
# 1. 检查是否已安装 Docker Compose V2
docker compose version

# 2. 如果没有，安装 Docker Compose V2 插件
sudo apt-get update
sudo apt-get install docker-compose-plugin

# 3. 使用新命令（注意是 docker compose，不是 docker-compose）
docker compose up -d
```

#### 方案 B: 使用兼容版本的配置文件

```bash
# 使用 docker-compose.v1.yaml（已优化兼容性）
docker-compose -f docker-compose.v1.yaml down
docker-compose -f docker-compose.v1.yaml up -d
```

#### 方案 C: 完全清理后重启

```bash
# 1. 停止并删除所有容器
docker-compose down

# 2. 删除旧镜像
docker rmi relay-pulse-monitor relaypulse-monitor ghcr.io/prehisle/relay-pulse

# 3. 清理系统（可选）
docker system prune -a

# 4. 重新拉取镜像
docker-compose pull

# 5. 强制重新创建
docker-compose up -d --force-recreate
```

#### 方案 D: 直接使用 docker run

```bash
# 创建网络和数据卷
docker network create relay-pulse-network
docker volume create relay-pulse-data

# 运行容器
docker run -d \
  --name relaypulse-monitor \
  --network relay-pulse-network \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/config/config.yaml:ro \
  -v relay-pulse-data:/data \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  ghcr.io/prehisle/relay-pulse:latest

# 查看日志
docker logs -f relaypulse-monitor

# 停止容器
docker stop relaypulse-monitor && docker rm relaypulse-monitor
```

> ⚠️ 仅挂载 `/data`（而不是整个 `/app`）可避免覆盖镜像内的最新二进制与静态资源，确保升级后容器立即获得新版本。

---

### 2. 配置文件未找到

**错误信息**:
```
open -config: no such file or directory
```

**原因**: entrypoint 脚本传递了错误的命令行参数

**解决方案**: 升级到最新版本的镜像（已修复）

```bash
docker pull ghcr.io/prehisle/relay-pulse:latest
docker-compose up -d
```

---

### 3. ARM64 架构镜像不存在

**错误信息**:
```
no matching manifest for linux/arm64/v8
```

**原因**: 预构建镜像可能只有 AMD64 版本

**解决方案**: 本地构建

```bash
# 修改 docker-compose.yaml，取消注释 build 配置
docker-compose build
docker-compose up -d
```

---

### 4. 数据库权限错误

**错误信息**:
```
unable to open database file
```

**原因**: SQLite 数据库文件权限不足

**解决方案**:

```bash
# 检查数据卷权限
docker volume inspect relay-pulse-data

# 重新创建数据卷
docker-compose down
docker volume rm relay-pulse-data
docker-compose up -d
```

---

### 5. 端口冲突

**错误信息**:
```
port is already allocated
```

**原因**: 8080 端口被占用

**解决方案**:

```bash
# 查看占用端口的进程
sudo netstat -tulpn | grep 8080
# 或
sudo lsof -i :8080

# 修改 docker-compose.yaml 使用其他端口
ports:
  - "8888:8080"  # 改为 8888
```

---

## 版本兼容性

| 组件 | 推荐版本 | 最低版本 |
|------|---------|---------|
| Docker | 24.0+ | 20.10+ |
| Docker Compose | V2 (2.20+) | V1 (1.29.2) |
| Go | 1.24 | 1.21 |
| Node.js | 20.x | 18.x |

---

## 健康检查

```bash
# 1. 检查容器状态
docker ps -a | grep relaypulse

# 2. 查看容器日志
docker logs relaypulse-monitor

# 3. 测试健康检查端点
curl http://localhost:8080/health

# 4. 测试 API 端点
curl http://localhost:8080/api/status

# 5. 进入容器调试
docker exec -it relaypulse-monitor sh

# 6. 查看容器配置
docker inspect relaypulse-monitor
```

---

## 性能优化

### 资源限制

在 docker-compose.yaml 中添加：

```yaml
services:
  monitor:
    deploy:
      resources:
        limits:
          cpus: '1'
          memory: 512M
        reservations:
          cpus: '0.5'
          memory: 256M
```

### 日志轮转

```yaml
services:
  monitor:
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

---

## 获取帮助

如果以上方案都无法解决问题：

1. 查看完整日志: `docker logs relaypulse-monitor > error.log`
2. 提交 Issue: https://github.com/prehisle/relay-pulse/issues
3. 包含以下信息:
   - 操作系统和版本
   - Docker 版本: `docker version`
   - Docker Compose 版本: `docker-compose version`
   - 错误日志
   - docker-compose.yaml 配置

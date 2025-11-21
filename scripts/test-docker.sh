#!/bin/bash
set -e

echo "================================"
echo "Docker 构建和测试脚本"
echo "================================"

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 镜像名称
IMAGE_NAME="llm-monitor-test"
CONTAINER_NAME="llm-monitor-test-container"

# 清理函数
cleanup() {
    echo -e "\n${YELLOW}正在清理测试环境...${NC}"
    docker stop $CONTAINER_NAME 2>/dev/null || true
    docker rm $CONTAINER_NAME 2>/dev/null || true
}

# 捕获退出信号
trap cleanup EXIT

echo -e "\n${YELLOW}步骤 1/5: 构建 Docker 镜像${NC}"
docker build -t $IMAGE_NAME .

echo -e "\n${GREEN}✓ 镜像构建成功${NC}"

echo -e "\n${YELLOW}步骤 2/5: 检查镜像大小${NC}"
docker images $IMAGE_NAME --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}"

echo -e "\n${YELLOW}步骤 3/5: 启动容器${NC}"
docker run -d \
  --name $CONTAINER_NAME \
  -p 8080:8080 \
  -e MONITOR_88CODE_CC_API_KEY="sk-test-key" \
  $IMAGE_NAME

echo -e "${GREEN}✓ 容器启动成功${NC}"

echo -e "\n${YELLOW}步骤 4/5: 等待服务启动 (5秒)${NC}"
sleep 5

echo -e "\n${YELLOW}步骤 5/5: 测试健康检查端点${NC}"
if curl -f http://localhost:8080/health; then
    echo -e "\n${GREEN}✓ 健康检查通过${NC}"
else
    echo -e "\n${RED}✗ 健康检查失败${NC}"
    echo -e "\n${YELLOW}容器日志:${NC}"
    docker logs $CONTAINER_NAME
    exit 1
fi

echo -e "\n${YELLOW}查看容器日志 (最后 20 行):${NC}"
docker logs --tail 20 $CONTAINER_NAME

echo -e "\n${GREEN}================================${NC}"
echo -e "${GREEN}Docker 测试完成!${NC}"
echo -e "${GREEN}================================${NC}"
echo -e "\n提示: 容器将在脚本退出时自动清理"
echo "如需手动清理，运行: docker stop $CONTAINER_NAME && docker rm $CONTAINER_NAME"

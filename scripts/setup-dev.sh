#!/bin/bash
# 开发环境设置脚本
# 解决 Go embed 不支持符号链接的问题

set -e

echo "🔧 设置开发环境..."
echo ""

# 1. 检查是否在项目根目录
if [ ! -f "go.mod" ]; then
    echo "❌ 请在项目根目录运行此脚本"
    exit 1
fi

# 2. 构建前端（如果需要）
if [ ! -d "frontend/dist" ] || [ "$1" = "--rebuild-frontend" ]; then
    echo "📦 构建前端..."
    cd frontend
    npm install
    npm run build
    cd ..
    echo "✅ 前端构建完成"
else
    echo "✅ 前端已构建（frontend/dist 存在）"
fi

# 3. 复制前端到 internal/api/frontend
echo "📋 复制前端文件到 internal/api/frontend..."
rm -rf internal/api/frontend
cp -r frontend internal/api/

echo "✅ 前端文件已复制到 internal/api/frontend"
echo ""

# 4. 创建配置文件（如果不存在）
if [ ! -f "config.yaml" ]; then
    echo "⚙️  创建配置文件..."
    cp config.yaml.example config.yaml
    echo "✅ 已从 config.yaml.example 创建 config.yaml"
    echo "⚠️  请编辑 config.yaml 并设置 API 密钥"
else
    echo "✅ config.yaml 已存在"
fi

echo ""
echo "🎉 开发环境设置完成！"
echo ""
echo "下一步："
echo "  1. 编辑 config.yaml 设置 API 密钥"
echo "  2. 运行 ./dev.sh 启动开发服务器（带热重载）"
echo "  3. 或者运行 go run cmd/server/main.go"
echo ""
echo "注意："
echo "  - Go embed 不支持符号链接，因此我们复制了 frontend 目录"
echo "  - 每次修改前端代码后，需要重新运行此脚本：./scripts/setup-dev.sh --rebuild-frontend"
echo "  - internal/api/frontend 已添加到 .gitignore，不会被提交"
echo ""

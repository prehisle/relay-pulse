# Service Horizon 前端

LLM 服务监控系统的前端界面，使用 React + TypeScript + TailwindCSS 构建。

## 功能特性

- 📊 **双视图模式**: 表格视图和卡片视图
- 🔍 **智能筛选**: 按服务商和服务类型筛选
- 📅 **时间范围**: 支持 24h、7d、15d、30d
- 📈 **热力图**: GitHub 风格的状态历史展示
- 🎯 **实时统计**: 正常运行数和异常告警数
- 🔄 **排序功能**: 按服务商、服务类型、状态、可用率排序
- 💡 **悬浮提示**: 鼠标悬停显示详细信息

## 技术栈

- **框架**: React 18 + TypeScript
- **构建工具**: Vite
- **样式**: TailwindCSS 4
- **图标**: lucide-react
- **HTTP**: Fetch API

## 项目结构

```
frontend/
├── src/
│   ├── components/       # React 组件
│   │   ├── Header.tsx
│   │   ├── Controls.tsx
│   │   ├── StatusTable.tsx
│   │   ├── StatusCard.tsx
│   │   ├── StatusDot.tsx
│   │   ├── HeatmapBlock.tsx
│   │   └── Tooltip.tsx
│   ├── hooks/           # 自定义 Hooks
│   │   └── useMonitorData.ts
│   ├── types/           # TypeScript 类型定义
│   │   └── index.ts
│   ├── constants/       # 常量配置
│   │   └── index.ts
│   ├── App.tsx          # 主应用组件
│   ├── main.tsx         # 应用入口
│   └── index.css        # 全局样式
├── .env.development     # 开发环境变量
├── .env.production      # 生产环境变量
└── package.json
```

## 快速开始

### 安装依赖

```bash
npm install
```

### 开发模式

```bash
npm run dev
```

访问 http://localhost:5173

### 生产构建

```bash
npm run build
```

构建产物位于 `dist/` 目录

### 预览生产版本

```bash
npm run preview
```

## 环境变量

在 `.env.development` 或 `.env.production` 中配置：

```env
VITE_API_BASE_URL=http://localhost:8080
```

## API 对接

前端通过 `GET /api/status` 接口获取监控数据：

- 参数: `period` (24h/7d/15d/30d), `provider` (服务商), `service` (服务类型)
- 返回: `{ meta: {...}, data: [{provider, service, current_status, timeline}] }`

## 开发说明

### 添加新服务商

编辑 `src/constants/index.ts` 的 `PROVIDERS` 数组：

```typescript
export const PROVIDERS: Provider[] = [
  { id: 'new-provider', name: 'New Provider', services: ['cc', 'cx'] },
  // ...
];
```

### 修改时间范围

编辑 `src/constants/index.ts` 的 `TIME_RANGES` 数组：

```typescript
export const TIME_RANGES: TimeRange[] = [
  { id: '1h', label: '近1小时', points: 60, unit: 'hour' },
  // ...
];
```

## 浏览器支持

- Chrome/Edge (最新版)
- Firefox (最新版)
- Safari (最新版)

## License

MIT

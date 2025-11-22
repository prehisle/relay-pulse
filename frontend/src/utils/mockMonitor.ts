import { PROVIDERS, TIME_RANGES } from '../constants';
import type { ProcessedMonitorData, StatusKey, StatusCounts } from '../types';

/**
 * 模拟数据生成器 - 完全复刻 docs/front.jsx 的逻辑
 * 用于演示和本地开发
 */
export function fetchMockMonitorData(timeRangeId: string): Promise<ProcessedMonitorData[]> {
  return new Promise((resolve) => {
    setTimeout(() => {
      // 默认使用 24h 范围，避免返回空数据
      const rangeConfig = TIME_RANGES.find(r => r.id === timeRangeId) || TIME_RANGES[0];
      if (!rangeConfig) {
        console.error(`Invalid timeRangeId: ${timeRangeId}, falling back to default`);
        resolve([]);
        return;
      }

      const count = rangeConfig.points;
      const data: ProcessedMonitorData[] = [];

      PROVIDERS.forEach((provider, providerIndex) => {
        provider.services.forEach((service) => {
          // 生成历史数据点
          const history = Array.from({ length: count }).map((_, index) => {
            const rand = Math.random();
            let statusKey: StatusKey = 'AVAILABLE';

            // 与 docs/front.jsx 完全一致的状态分配逻辑，并添加缺失数据
            if (rand > 0.98) statusKey = 'MISSING';        // 2% 概率缺失
            else if (rand > 0.95) statusKey = 'UNAVAILABLE';  // 3% 概率不可用
            else if (rand > 0.85) statusKey = 'DEGRADED';     // 10% 概率降级

            // 生成模拟延迟（缺失数据延迟为0）
            const latency = statusKey === 'MISSING' ? 0 : 180 + Math.floor(Math.random() * 220);

            // 根据状态生成模拟可用率
            let availability: number;
            if (statusKey === 'MISSING') {
              availability = -1;
            } else if (statusKey === 'AVAILABLE') {
              availability = 80 + Math.random() * 20;  // 80-100%
            } else if (statusKey === 'DEGRADED') {
              availability = 60 + Math.random() * 20;  // 60-80%
            } else {
              availability = Math.random() * 60;        // 0-60%
            }

            const timestampMs = Date.now() - (count - index) * (rangeConfig.unit === 'hour' ? 3600000 : 86400000);

            // 模拟状态计数（单次探测，所以只有一个状态为 1）
            // 并根据状态类型模拟细分统计（每次只选择一个细分）
            const statusCounts: StatusCounts = {
              available: statusKey === 'AVAILABLE' ? 1 : 0,
              degraded: statusKey === 'DEGRADED' ? 1 : 0,
              unavailable: statusKey === 'UNAVAILABLE' ? 1 : 0,
              missing: statusKey === 'MISSING' ? 1 : 0,
              slow_latency: 0,
              rate_limit: 0,
              server_error: 0,
              client_error: 0,
              auth_error: 0,
              invalid_request: 0,
              network_error: 0,
              content_mismatch: 0,
            };

            // 为黄色和红色状态随机选择一个细分原因（模拟真实后端行为）
            if (statusKey === 'DEGRADED') {
              if (Math.random() > 0.5) {
                statusCounts.slow_latency = 1;
              } else {
                statusCounts.rate_limit = 1;
              }
            } else if (statusKey === 'UNAVAILABLE') {
              const rand = Math.random();
              if (rand > 0.75) {
                statusCounts.server_error = 1;
              } else if (rand > 0.5) {
                statusCounts.client_error = 1;
              } else if (rand > 0.25) {
                statusCounts.network_error = 1;
              } else {
                statusCounts.content_mismatch = 1;
              }
            }

            return {
              index,
              status: statusKey,
              timestamp: new Date(timestampMs).toISOString(),
              timestampNum: Math.floor(timestampMs / 1000),  // Unix 时间戳（秒）
              latency,
              availability,
              statusCounts,
            };
          });

          const currentStatus = history[history.length - 1].status;

          // 计算可用率（AVAILABLE 和 DEGRADED 都算成功，与后端逻辑一致）
          const uptimeScore = history.reduce((acc, point) => {
            if (point.status === 'AVAILABLE' || point.status === 'DEGRADED') return acc + 1;  // 100%
            if (point.status === 'MISSING') return acc + 0.5;  // 50%
            return acc;  // 0% (UNAVAILABLE)
          }, 0);
          const uptime = history.length > 0
            ? parseFloat((uptimeScore / history.length * 100).toFixed(2))
            : 0;

          // 模拟通道名（按照 provider 分配）
          const channels = ['vip-channel', 'standard-channel', 'test-channel'];
          const channel = channels[providerIndex % channels.length];

          // 模拟分类和赞助者
          const categories: Array<'commercial' | 'public'> = ['commercial', 'public'];
          const category = categories[providerIndex % 2];
          const sponsors = ['团队自有', '社区赞助', 'duckcoding官方', '示例数据'];
          const sponsor = sponsors[providerIndex % sponsors.length];

          // 最后一次检测信息
          const lastCheckTimestamp = Math.floor(Date.now() / 1000);
          const lastCheckLatency = 180 + Math.floor(Math.random() * 220);

          data.push({
            id: `${provider.id}-${service}`,
            providerId: provider.id,
            providerName: provider.name,
            serviceType: service,
            category,
            sponsor,
            channel,
            history,
            currentStatus,
            uptime,
            lastCheckTimestamp,
            lastCheckLatency
          });
        });
      });

      resolve(data);
    }, 600); // 与 docs/front.jsx 一致的延迟时间
  });
}

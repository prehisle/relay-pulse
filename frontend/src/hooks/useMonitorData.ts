import { useState, useEffect, useMemo } from 'react';
import type {
  ApiResponse,
  ProcessedMonitorData,
  SortConfig,
  StatusKey,
} from '../types';
import { API_BASE_URL, STATUS } from '../constants';

// 导入 STATUS_MAP
const statusMap: Record<number, StatusKey> = {
  1: 'AVAILABLE',
  2: 'DEGRADED',
  0: 'UNAVAILABLE',
};

interface UseMonitorDataOptions {
  timeRange: string;
  filterService: string;
  filterProvider: string;
  sortConfig: SortConfig;
}

export function useMonitorData({
  timeRange,
  filterService,
  filterProvider,
  sortConfig,
}: UseMonitorDataOptions) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [rawData, setRawData] = useState<ProcessedMonitorData[]>([]);

  // 数据获取
  useEffect(() => {
    const fetchData = async () => {
      setLoading(true);
      setError(null);

      try {
        const url = `${API_BASE_URL}/api/status?period=${timeRange}`;
        const response = await fetch(url);

        if (!response.ok) {
          throw new Error(`HTTP error! status: ${response.status}`);
        }

        const json: ApiResponse = await response.json();

        // 转换为前端数据格式
        const processed: ProcessedMonitorData[] = json.data.map((item) => {
          const history = item.timeline.map((point, index) => ({
            index,
            status: statusMap[point.status] || 'UNAVAILABLE',
            timestamp: point.time,
            latency: point.latency,
          }));

          const currentStatus = item.current_status
            ? statusMap[item.current_status.status] || 'UNAVAILABLE'
            : 'UNAVAILABLE';

          // 计算可用率
          const availableCount = history.filter((h) => h.status === 'AVAILABLE').length;
          const uptime = parseFloat(((availableCount / history.length) * 100).toFixed(1));

          return {
            id: `${item.provider}-${item.service}`,
            providerId: item.provider,
            providerName: item.provider,
            serviceType: item.service,
            history,
            currentStatus,
            uptime,
          };
        });

        setRawData(processed);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Unknown error');
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, [timeRange]);

  // 数据过滤和排序
  const processedData = useMemo(() => {
    let filtered = rawData.filter((item) => {
      const matchService = filterService === 'all' || item.serviceType === filterService;
      const matchProvider = filterProvider === 'all' || item.providerId === filterProvider;
      return matchService && matchProvider;
    });

    if (sortConfig.key) {
      filtered.sort((a, b) => {
        let aValue: any = a[sortConfig.key as keyof ProcessedMonitorData];
        let bValue: any = b[sortConfig.key as keyof ProcessedMonitorData];

        if (sortConfig.key === 'currentStatus') {
          aValue = STATUS[a.currentStatus].weight;
          bValue = STATUS[b.currentStatus].weight;
        }

        if (aValue < bValue) return sortConfig.direction === 'asc' ? -1 : 1;
        if (aValue > bValue) return sortConfig.direction === 'asc' ? 1 : -1;
        return 0;
      });
    }

    return filtered;
  }, [rawData, filterService, filterProvider, sortConfig]);

  // 统计数据
  const stats = useMemo(() => {
    const total = processedData.length;
    const healthy = processedData.filter((i) => i.currentStatus === 'AVAILABLE').length;
    const issues = total - healthy;
    return { total, healthy, issues };
  }, [processedData]);

  return {
    loading,
    error,
    data: processedData,
    stats,
    refetch: () => {
      // 触发重新获取
      setRawData([]);
      setLoading(true);
    },
  };
}

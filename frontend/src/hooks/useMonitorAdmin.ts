import { useState, useCallback, useEffect, useRef } from 'react';
import { apiGet, apiPost, apiPut, apiDelete, ApiError } from '../utils/apiClient';
import type {
  MonitorSummary,
  MonitorFile,
  AdminMonitorListResponse,
  AdminMonitorDetailResponse,
  AdminMonitorLogsResponse,
  ProbeHistoryEntry,
  ProbeTarget,
  MonitorToggleRequest,
} from '../types/monitor';

/** 父通道在按 target 分桶的 probe 状态里使用的固定 key。 */
export const PARENT_TARGET_KEY = '';

const SEARCH_DEBOUNCE_MS = 300;

export interface ProbeResult {
  probeId: string;
  probeStatus: number;
  subStatus: string;
  httpCode: number;
  latency: number;
  errorMessage: string;
  responseSnippet: string;
  /** 本次实际请求对应的可复制 curl 命令（默认脱敏，密钥用 $RP_API_KEY 占位）。 */
  curl: string;
  /** 本次探测是否经通道配置的代理（仅管理员路径会走代理）。 */
  viaProxy: boolean;
}

export function useMonitorAdmin(token: string) {
  const [monitors, setMonitors] = useState<MonitorSummary[]>([]);
  const [total, setTotal] = useState(0);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Filters
  const [boardFilter, setBoardFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  // searchQuery 跟随输入即时更新（供受控输入框回显），debouncedSearchQuery 才驱动请求。
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedSearchQuery, setDebouncedSearchQuery] = useState('');
  const listAbortRef = useRef<AbortController | null>(null);

  // Detail
  const [selectedMonitor, setSelectedMonitor] = useState<MonitorFile | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [probeTargets, setProbeTargets] = useState<ProbeTarget[]>([]);
  const [detailLoadingId, setDetailLoadingId] = useState<string | null>(null);
  const detailAbortRef = useRef<AbortController | null>(null);

  // Probe：父通道与各子通道可独立探测，状态按 target key 分桶（key=target model，
  // 父通道用 PARENT_TARGET_KEY=''），避免多按钮互相覆盖结果。
  // 声明须置于 fetchDetail 之上——后者切换详情时会清空这些桶，先用后声明会触发
  // react-hooks/immutability（"Cannot access variable before it is declared"）。
  const [probingTargets, setProbingTargets] = useState<Record<string, boolean>>({});
  const [probeResults, setProbeResults] = useState<Record<string, ProbeResult>>({});
  const [probeErrors, setProbeErrors] = useState<Record<string, string>>({});

  const authHeaders = useCallback((): HeadersInit => ({
    Authorization: `Bearer ${token}`,
  }), [token]);

  // 输入做 debounce：稳定 300ms 后才更新驱动请求的值（与 useAdmin 的申请列表同规格），
  // 避免每个键入都打一次列表接口。
  useEffect(() => {
    const timer = setTimeout(() => {
      setDebouncedSearchQuery(searchQuery.trim());
    }, SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  // Fetch list
  const fetchList = useCallback(async () => {
    if (!token) return;
    // 中止上一条在途列表请求：宽关键词（匹配多、后端要批量注入探测快照）比窄关键词慢，
    // 不中止会让迟到的旧响应覆盖新筛选结果（表现为输入后列表纹丝不动，需手动刷新）。
    listAbortRef.current?.abort();
    const ac = new AbortController();
    listAbortRef.current = ac;
    setIsLoading(true);
    setError(null);

    try {
      const params = new URLSearchParams();
      if (boardFilter) params.set('board', boardFilter);
      if (statusFilter) params.set('status', statusFilter);
      if (debouncedSearchQuery) params.set('q', debouncedSearchQuery);

      const qs = params.toString();
      const resp = await apiGet<AdminMonitorListResponse>(
        `/api/admin/monitors${qs ? '?' + qs : ''}`,
        { headers: authHeaders(), signal: ac.signal },
      );
      if (ac.signal.aborted) return;
      setMonitors(resp.monitors || []);
      setTotal(resp.total);
    } catch (e) {
      if (ac.signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) return;
      setError(e instanceof ApiError ? e.message : '加载失败');
    } finally {
      // 只有仍是当前请求时才收起加载态，被后续请求顶掉的旧请求不碰
      if (listAbortRef.current === ac) setIsLoading(false);
    }
  }, [token, boardFilter, statusFilter, debouncedSearchQuery, authHeaders]);

  // 写操作（create/update/delete/toggle）成功后的列表刷新必须用最新筛选参数：
  // 它们 await 期间用户可能已改搜索词，闭包捕获的旧 fetchList 会按过期条件取数、
  // 还会反过来中止在途的新搜索，故经 ref 始终调用最新版本。
  const fetchListRef = useRef(fetchList);
  useEffect(() => {
    fetchListRef.current = fetchList;
  }, [fetchList]);

  // 卸载时中止在途列表请求，并把写后刷新降级为 no-op：在途写操作若在卸载后才返回，
  // 不应再为已离开的页面发起新的列表查询（后端要跑批量快照注入，不便宜）。
  useEffect(() => () => {
    fetchListRef.current = async () => {};
    listAbortRef.current?.abort();
  }, []);

  // 手动刷新：立即按输入框现值取数，不等防抖窗口。关键词有变时更新防抖值、
  // 由取数 effect 接手发请求；无变时直接重拉一次。
  const refreshList = useCallback(() => {
    const q = searchQuery.trim();
    if (q !== debouncedSearchQuery) {
      setDebouncedSearchQuery(q);
    } else {
      fetchList();
    }
  }, [searchQuery, debouncedSearchQuery, fetchList]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 挂载即取数：fetchList 在 await 前同步置 loading/清错误为有意，非派生 state
    if (token) fetchList();
  }, [token, fetchList]);

  // Fetch templates
  const fetchTemplates = useCallback(async (): Promise<string[]> => {
    if (!token) return [];

    try {
      const resp = await apiGet<{ templates: string[] }>(
        '/api/admin/templates',
        { headers: authHeaders() },
      );
      return resp.templates || [];
    } catch {
      return [];
    }
  }, [token, authHeaders]);

  // Fetch detail
  const fetchDetail = useCallback(async (key: string) => {
    if (!token) return;
    detailAbortRef.current?.abort(); // 中止上一条在途详情，防止迟到响应覆盖新选中项
    const ac = new AbortController();
    detailAbortRef.current = ac;
    setDetailLoadingId(key);
    setError(null);

    try {
      const resp = await apiGet<AdminMonitorDetailResponse>(
        `/api/admin/monitors/${key}`,
        { headers: authHeaders(), signal: ac.signal },
      );
      if (ac.signal.aborted) return;
      // 详情成功返回后再清空上一通道的 probe 结果，避免串台；被取消的旧请求不清，
      // 让当前显示的通道保留其探测结果直到新详情真正就绪。
      setProbingTargets({});
      setProbeResults({});
      setProbeErrors({});
      setSelectedMonitor(resp.monitor);
      setProbeTargets(resp.probe_targets || []);
      setSelectedKey(key);
    } catch (e) {
      if (ac.signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) return;
      setError(e instanceof ApiError ? e.message : '加载详情失败');
    } finally {
      if (detailAbortRef.current === ac) setDetailLoadingId(null);
    }
  }, [token, authHeaders]);

  // 供切 tab / 返回列表时中止在途详情请求
  const cancelDetail = useCallback(() => {
    detailAbortRef.current?.abort();
    detailAbortRef.current = null;
    setDetailLoadingId(null);
  }, []);

  // Create
  const createMonitor = useCallback(async (file: MonitorFile) => {
    if (!token) return;
    setError(null);

    try {
      await apiPost<AdminMonitorDetailResponse>(
        '/api/admin/monitors',
        file,
        { headers: authHeaders() },
      );
      fetchListRef.current();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : '创建失败';
      setError(msg);
      throw e;
    }
  }, [token, authHeaders]);

  // Update
  const updateMonitor = useCallback(async (key: string, file: MonitorFile, revision: number) => {
    if (!token) return;
    setError(null);

    try {
      await apiPut<{ monitor: MonitorFile }>(
        `/api/admin/monitors/${key}`,
        { revision, monitor: file },
        { headers: authHeaders() },
      );
      fetchListRef.current();
      // 重新拉详情：刷新 probe_targets（保存可能改了子通道 model/template）并清空旧探测结果，
      // 避免查看态用陈旧 target 测到已不存在的 model。
      await fetchDetail(key);
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : '更新失败';
      setError(msg);
      throw e;
    }
  }, [token, authHeaders, fetchDetail]);

  // Delete
  const deleteMonitor = useCallback(async (key: string) => {
    if (!token) return;
    setError(null);

    try {
      await apiDelete(`/api/admin/monitors/${key}`, { headers: authHeaders() });
      setSelectedMonitor(null);
      setSelectedKey(null);
      fetchListRef.current();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '删除失败');
    }
  }, [token, authHeaders]);

  // Toggle
  //
  // scope='model' 时**无条件**带上 model_id，哪怕是空串——空串会被后端 400 拒掉，
  // 而「按 truthy 过滤掉空串」会让请求退化成旧形态、被当成切父通道执行：用户以为停一个
  // 子模型、实际停掉整条通道。宁可报错也不能猜意图。
  //
  // 这里只 setSelectedMonitor 不重拉详情：UI 的停用态读的是响应里的 monitors.d 原始行（权威且
  // 即时），而 probe_targets 的 disabled 是热更新后的运行时值，此刻重拉多半还没 reload 完，
  // 拿到的是另一种陈旧，不会更准。
  const toggleMonitor = useCallback(async (key: string, req: MonitorToggleRequest) => {
    if (!token) return;
    setError(null);

    try {
      const resp = await apiPost<{ monitor: MonitorFile }>(
        `/api/admin/monitors/${key}/toggle`,
        {
          field: req.field,
          value: req.value,
          ...(req.scope === 'model' ? { model_id: req.modelId } : {}),
        },
        { headers: authHeaders() },
      );
      setSelectedMonitor(resp.monitor);
      fetchListRef.current();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '切换失败');
    }
  }, [token, authHeaders]);

  const probeMonitor = useCallback(async (
    key: string,
    overrides?: { template?: string; base_url?: string; api_key?: string },
    targetModel = '',
  ): Promise<ProbeResult | null> => {
    if (!token) return null;
    const targetKey = targetModel || PARENT_TARGET_KEY;
    setProbingTargets(prev => ({ ...prev, [targetKey]: true }));
    setProbeErrors(prev => {
      const next = { ...prev };
      delete next[targetKey];
      return next;
    });

    try {
      const resp = await apiPost<{
        probe_id: string;
        probe_status: number;
        sub_status: string;
        http_code: number;
        latency: number;
        error_message: string;
        response_snippet: string;
        curl?: string;
        via_proxy?: boolean;
      }>(
        `/api/admin/monitors/${key}/probe`,
        { ...(overrides ?? {}), ...(targetModel ? { target_model: targetModel } : {}) },
        { headers: authHeaders() },
      );
      const result: ProbeResult = {
        probeId: resp.probe_id,
        probeStatus: resp.probe_status,
        subStatus: resp.sub_status,
        httpCode: resp.http_code,
        latency: resp.latency,
        errorMessage: resp.error_message,
        responseSnippet: resp.response_snippet,
        curl: resp.curl ?? '',
        viaProxy: resp.via_proxy ?? false,
      };
      setProbeResults(prev => ({ ...prev, [targetKey]: result }));
      return result;
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : '探测失败';
      setProbeErrors(prev => ({ ...prev, [targetKey]: msg }));
      return null;
    } finally {
      setProbingTargets(prev => ({ ...prev, [targetKey]: false }));
    }
  }, [token, authHeaders]);

  // Logs：拉取某监测项的探测历史记录（按 timestamp 倒序）。
  // since: Go duration (默认 "1h") 或 RFC3339；limit: 默认 200，上限 1000；model: 可选过滤。
  const fetchMonitorLogs = useCallback(async (
    key: string,
    opts?: { since?: string; limit?: number; model?: string },
  ): Promise<ProbeHistoryEntry[]> => {
    if (!token) return [];

    const params = new URLSearchParams();
    if (opts?.since) params.set('since', opts.since);
    if (opts?.limit != null) params.set('limit', String(opts.limit));
    if (opts?.model) params.set('model', opts.model);

    const qs = params.toString();
    const resp = await apiGet<AdminMonitorLogsResponse>(
      `/api/admin/monitors/${encodeURIComponent(key)}/logs${qs ? '?' + qs : ''}`,
      { headers: authHeaders() },
    );
    return resp.logs || [];
  }, [token, authHeaders]);

  return {
    monitors,
    total,
    isLoading,
    error,

    boardFilter,
    setBoardFilter,
    statusFilter,
    setStatusFilter,
    searchQuery,
    setSearchQuery,
    refreshList,

    selectedMonitor,
    selectedKey,
    probeTargets,
    setSelectedMonitor,
    setSelectedKey,
    detailLoadingId,
    fetchDetail,
    cancelDetail,
    fetchTemplates,
    createMonitor,
    updateMonitor,
    deleteMonitor,
    toggleMonitor,
    probeMonitor,
    probingTargets,
    probeResults,
    probeErrors,
    fetchMonitorLogs,
  };
}

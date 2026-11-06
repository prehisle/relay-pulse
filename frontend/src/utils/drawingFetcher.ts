import type { RpdiagDrawing } from '../types/monitor';
import { apiGet } from './apiClient';

/**
 * 画作列取数：表格里每个格子只报「我要这几个 task id」，这里攒一小会儿合并成一次
 * `/api/rpdiag-drawings` 请求（每次 ≤ MAX_IDS_PER_REQUEST 个），结果按 id 缓存在
 * 模块里——切时间范围、翻筛选导致格子重新挂载时不重复请求。
 *
 * 后端只认当前快照里出现过的 id、略过 rpdiag 还没公开的测试，所以「请求过却没返回」
 * 的 id 记为 null（这次没有画）。null 与还有一侧未出结果的画过一段时间才允许再问；
 * 两侧都已定局的画不再变，一直用缓存。
 */

const MAX_IDS_PER_REQUEST = 30;
const FLUSH_DELAY_MS = 40;
const MISSING_RETRY_MS = 5 * 60 * 1000;

type Entry = { drawing: RpdiagDrawing | null; at: number };

const cache = new Map<string, Entry>();
const queued = new Set<string>();
const inFlight = new Set<string>();
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | null = null;

function notify() {
  listeners.forEach((fn) => fn());
}

const FINAL_STATES = new Set(['done', 'failed', 'not_reached', 'none']);

function isSettled(drawing: RpdiagDrawing | null): boolean {
  return drawing !== null
    && FINAL_STATES.has(drawing.candidate.state)
    && (!drawing.official || FINAL_STATES.has(drawing.official.state));
}

function isFresh(id: string, now: number): boolean {
  const entry = cache.get(id);
  if (!entry) return false;
  return isSettled(entry.drawing) || now - entry.at < MISSING_RETRY_MS;
}

async function fetchBatch(ids: string[]) {
  const params = new URLSearchParams();
  ids.forEach((id) => params.append('task_ids', id));
  let items: RpdiagDrawing[] = [];
  try {
    const body = await apiGet<{ items?: RpdiagDrawing[] }>(`/api/rpdiag-drawings?${params.toString()}`);
    items = Array.isArray(body?.items) ? body.items : [];
  } catch {
    // 网络错误：这批记为「没有画」，过期后再试；画作列不值得为此报错。
  }
  const now = Date.now();
  const byId = new Map(items.map((d) => [d.task_id, d]));
  for (const id of ids) {
    cache.set(id, { drawing: byId.get(id) ?? null, at: now });
    inFlight.delete(id);
  }
  notify();
}

function flush() {
  timer = null;
  const ids = [...queued];
  queued.clear();
  ids.forEach((id) => inFlight.add(id));
  for (let i = 0; i < ids.length; i += MAX_IDS_PER_REQUEST) {
    void fetchBatch(ids.slice(i, i + MAX_IDS_PER_REQUEST));
  }
}

/** 登记要取的画；已缓存或在途的 id 自动跳过。 */
export function requestDrawings(ids: string[]) {
  const now = Date.now();
  let added = false;
  for (const id of ids) {
    if (isFresh(id, now) || inFlight.has(id) || queued.has(id)) continue;
    queued.add(id);
    added = true;
  }
  if (added && timer === null) {
    timer = setTimeout(flush, FLUSH_DELAY_MS);
  }
}

/** undefined＝还没取到；null＝取过了，这次测试没有可公开的画。 */
export function getDrawing(id: string): RpdiagDrawing | null | undefined {
  return cache.get(id)?.drawing;
}

export function subscribeDrawings(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** 仅测试用：清空模块状态。 */
export function resetDrawingFetcherForTest() {
  cache.clear();
  queued.clear();
  inFlight.clear();
  listeners.clear();
  if (timer !== null) clearTimeout(timer);
  timer = null;
}

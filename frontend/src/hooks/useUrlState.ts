import { useCallback, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { ViewMode, SortConfig, BoardFilter } from '../types';
import { HIDE_PRICE_COLUMN } from '../constants';

/**
 * URL 查询参数与状态同步的配置
 *
 * 需要同步的状态：
 * - period: 时间范围（默认 24h）
 * - provider: 服务商筛选
 * - service: 服务筛选
 * - channel: 渠道筛选
 * - category: 分类筛选
 * - view: 视图模式（默认 table）
 * - sort: 排序配置（格式：key_direction，如 uptime_desc）
 * - fav: 仅显示收藏（1=是，默认否）
 */

interface UrlState {
  timeRange: string;
  timeFilter: string | null; // 每日时段过滤：null=全天, "09:00-17:00"=自定义
  board: BoardFilter;        // 板块：hot/secondary/cold/all（默认 hot）
  filterProvider: string[];  // 多选服务商，空数组表示"全部"
  filterService: string[];   // 多选服务，空数组表示"全部"
  filterChannel: string[];   // 多选通道，空数组表示"全部"
  filterCategory: string[];  // 多选分类，空数组表示"全部"
  filterVendor: string[];    // 多选模型厂商，空数组表示"全部"
  showFavoritesOnly: boolean; // 仅显示收藏
  viewMode: ViewMode;
  sortConfig: SortConfig;
  isInitialSort: boolean;    // 是否为初始排序状态（URL 无 sort 参数）
}

interface UrlStateActions {
  setTimeRange: (value: string) => void;
  setTimeFilter: (value: string | null) => void; // 每日时段过滤
  setBoard: (value: BoardFilter) => void;        // 切换板块
  setFilterProvider: (value: string[]) => void;  // 多选服务商
  setFilterService: (value: string[]) => void;   // 多选服务
  setFilterChannel: (value: string[]) => void;   // 多选通道
  setFilterCategory: (value: string[]) => void;  // 多选分类
  setFilterVendor: (value: string[]) => void;    // 多选模型厂商
  setShowFavoritesOnly: (value: boolean) => void; // 仅显示收藏
  setViewMode: (value: ViewMode) => void;
  setSortConfig: (value: SortConfig) => void;
  /** 若 URL sort 参数当前是 priceRatio_*，将其删除（恢复初始置顶语义）。
   *  与 setSortConfig 不同：不会设置 hasManualSort，因此置顶恢复后仍允许首屏置顶生效。
   *  用于 runtime 隐藏价格列后，清理用户之前留下的"按价格排序"链接。 */
  clearPriceRatioSort: () => void;
  /** 同 clearPriceRatioSort，但针对 qualityScore_* —— rpdiag 关闭（私有部署）后清理质量列旧排序链接。 */
  clearQualityScoreSort: () => void;
  /** 同上，针对 modelVendor_* —— 数据里没有任何厂商声明时厂商列消失，清掉旧排序链接。 */
  clearModelVendorSort: () => void;
  enterFavoritesMode: () => void;  // 进入收藏模式（保存快照并清空筛选）
  exitFavoritesMode: () => void;   // 退出收藏模式（恢复快照）
}

// 默认值
const DEFAULTS = {
  timeRange: '90m',
  timeFilter: null as string | null, // 全天（无过滤）
  board: 'hot' as BoardFilter,       // 默认热板
  filterProvider: [] as string[],  // 空数组表示"全部"
  filterService: [] as string[],   // 空数组表示"全部"
  filterChannel: [] as string[],   // 空数组表示"全部"
  filterCategory: [] as string[],  // 空数组表示"全部"
  filterVendor: [] as string[],    // 空数组表示"全部"
  showFavoritesOnly: false,        // 默认显示全部
  viewMode: 'table' as ViewMode,
  sortKey: 'uptime',
  sortDirection: 'desc' as const,
};

// 默认排序参数值（用于判断是否需要保留 URL 参数）
const DEFAULT_SORT_PARAM = `${DEFAULTS.sortKey}_${DEFAULTS.sortDirection}`;

// URL 参数名映射
const PARAM_KEYS = {
  timeRange: 'period',
  timeFilter: 'tf',  // 时段过滤：简短 key 保持 URL 简洁
  board: 'board',    // 板块：hot/secondary/cold/all
  filterProvider: 'provider',
  filterService: 'service',
  filterChannel: 'channel',
  filterCategory: 'category',
  filterVendor: 'vendor',
  showFavoritesOnly: 'fav',  // 仅显示收藏
  viewMode: 'view',
  sort: 'sort',
};

// 收藏模式快照存储 key（sessionStorage）
const SNAPSHOT_KEY = 'relay-pulse:v1:list-state';

// 快照数据结构
interface ListStateSnapshot {
  version: 1;
  filterProvider: string[];
  filterService: string[];
  filterChannel: string[];
  filterCategory: string[];
  /** 2026-08 加入的厂商筛选。**故意留在 version 1**：上一会话存下的旧快照没有这个键，
   *  升 version 会让那份快照整体作废（用户退出收藏模式时筛选凭空清空）。
   *  解析走 parseListStateSnapshot，缺失/非法一律降级成空数组而不是整份丢弃。 */
  filterVendor: string[];
}

/** 从 sessionStorage 原始值解析快照：结构不对返回 null；只有 filterVendor 这一新增键
 *  允许缺失（旧会话快照），其余四个数组仍是必需——它们缺失说明这根本不是本结构。 */
function parseListStateSnapshot(raw: string | null): ListStateSnapshot | null {
  if (!raw) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;
  const candidate = parsed as Record<string, unknown>;
  if (candidate.version !== 1) return null;

  const asStringArray = (value: unknown): string[] | null =>
    Array.isArray(value) && value.every((v) => typeof v === 'string') ? value : null;

  const filterProvider = asStringArray(candidate.filterProvider);
  const filterService = asStringArray(candidate.filterService);
  const filterChannel = asStringArray(candidate.filterChannel);
  const filterCategory = asStringArray(candidate.filterCategory);
  if (!filterProvider || !filterService || !filterChannel || !filterCategory) return null;

  return {
    version: 1,
    filterProvider,
    filterService,
    filterChannel,
    filterCategory,
    filterVendor: asStringArray(candidate.filterVendor) ?? [],
  };
}

/**
 * 判断 URL sort 原始参数是否应被忽略
 * 当前仅用于：价格列被 HIDE_PRICE_COLUMN 隐藏时，旧链接的 priceRatio_* 视为无效
 */
function isIgnorableSortParam(param: string | null): boolean {
  if (!param) return false;
  if (!HIDE_PRICE_COLUMN) return false;
  const lastUnderscore = param.lastIndexOf('_');
  const key = lastUnderscore === -1 ? param : param.substring(0, lastUnderscore);
  return key === 'priceRatio';
}

/**
 * 解析排序参数
 * 格式：key_direction，如 uptime_desc、latency_asc
 */
function parseSortParam(param: string | null): SortConfig {
  if (!param || isIgnorableSortParam(param)) {
    return { key: DEFAULTS.sortKey, direction: DEFAULTS.sortDirection };
  }

  const lastUnderscore = param.lastIndexOf('_');
  if (lastUnderscore === -1) {
    return { key: param, direction: DEFAULTS.sortDirection };
  }

  const key = param.substring(0, lastUnderscore);
  const direction = param.substring(lastUnderscore + 1);

  if (direction === 'asc' || direction === 'desc') {
    // 旧排序 key 迁移到 lastCheck（latency 保留原语义，不迁移）
    const migratedKey = key === 'currentStatus' ? 'lastCheck' : key;
    return { key: migratedKey, direction };
  }

  return { key: param, direction: DEFAULTS.sortDirection };
}

/**
 * 序列化排序配置为 URL 参数
 */
function serializeSortConfig(config: SortConfig): string {
  return `${config.key}_${config.direction}`;
}

/**
 * 双向同步 URL 查询参数和组件状态的 Hook
 *
 * 特性：
 * - 初始化时从 URL 恢复状态
 * - 状态变化时自动更新 URL
 * - 默认值不会出现在 URL 中（保持 URL 简洁）
 * - 使用 replace 模式避免污染浏览器历史
 */
export function useUrlState(): [UrlState, UrlStateActions] {
  const [searchParams, setSearchParams] = useSearchParams();

  // 同一批次内已写入、但组件还没重新渲染的参数快照（null=本批次尚无写入）。
  //
  // 为什么需要它：react-router 的 setSearchParams(prev => ...) 里的 prev 是**闭包捕获的
  // 上一次渲染值**，而且每次调用立刻 navigate——它不是 React setState 那种会把上一个
  // updater 的结果喂给下一个的队列。于是同一个事件/同一次 commit 里连续写两次 URL 时，
  // 第二次会拿着不含第一次改动的旧值再导航一遍，把第一次的改动整个抹掉。
  //
  // 实际踩到的两处：
  //   ① 移动端筛选抽屉「清空」一次调 6 个 setter → 只有最后一个真正生效，其余被带回来；
  //   ② 隐藏列/关功能后的几个 URL 纠偏 effect（清排序、归一 board）落在同一次 commit 时互相回写。
  //
  // 修法：所有写入都经 commitParams 走这一个咽喉点，以「本批次已写入的值」而非渲染快照为基底，
  // 于是多次写入自然叠加。
  //
  // ⚠️ 边界要说准：microtask 覆盖的是**同一个同步调用栈**（一个事件处理函数、一次 passive
  // effect flush 内顺序跑完的那些 effect），它不是 React/Router 提供的路由事务标识。
  // 已知覆盖不到、且当前仓库不存在的形状：同一同步栈里夹一次 flushSync 外部导航后再写本 hook，
  // 或导航被 blocker 拒绝——那时基底会比真实 URL 旧。真出现这类调用，得改成同时记住
  // 「本批起始 prev」与「累计 next」，发现 prev 两者都不是就说明 URL 被外部推进过、应改用新 prev。
  const pendingParamsRef = useRef<URLSearchParams | null>(null);

  const commitParams = useCallback((mutate: (params: URLSearchParams) => void) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(pendingParamsRef.current ?? prev);
      mutate(next);
      if (pendingParamsRef.current === null) {
        // 本批次第一次写入：排一个 microtask 在批次末尾丢弃基底，
        // 避免它跨批次残留、把用户后来删掉的参数又复活。
        queueMicrotask(() => {
          pendingParamsRef.current = null;
        });
      }
      pendingParamsRef.current = next;
      return next;
    }, { replace: true });
  }, [setSearchParams]);
  // 会话态标记：用户是否在本次会话中手动点击过排序
  // 刷新页面后会重置为 false，允许置顶恢复
  const [hasManualSort, setHasManualSort] = useState(false);

  // 规范化函数：小写（用于 provider, service, category）
  const normalizeLower = useCallback((value: string) => value.trim().toLowerCase(), []);
  // 规范化函数：保留原始大小写（用于 channel，因为 channel 值来自后端数据）
  const normalizePreserveCase = useCallback((value: string) => value.trim(), []);

  // 从 URL 读取当前状态
  const state = useMemo<UrlState>(() => {
    // 验证 viewMode 参数，防止 URL 被篡改导致内容区空白
    const rawViewMode = searchParams.get(PARAM_KEYS.viewMode);
    const viewMode: ViewMode = (rawViewMode === 'table' || rawViewMode === 'grid')
      ? rawViewMode
      : DEFAULTS.viewMode;

    // 解析多选参数的通用函数（支持逗号分隔）
    // 向后兼容：过滤掉 'all'（旧版"全部"语义），去重并排序
    const parseArrayParam = (
      key: string,
      normalizer: (value: string) => string
    ): string[] => {
      const param = searchParams.get(key);
      if (!param) return [];
      return Array.from(new Set(
        param
          .split(',')
          .map(normalizer)
          .filter(s => s && s.toLowerCase() !== 'all')  // 过滤空值和旧的 'all'
      )).sort();
    };

    // 获取 sort 参数
    const rawSortParam = searchParams.get(PARAM_KEYS.sort);
    // 隐藏价格列时，旧链接的 priceRatio_* 参数视为无效（保持赞助置顶语义）
    const hasSortParam = Boolean(rawSortParam && rawSortParam.trim() && !isIgnorableSortParam(rawSortParam));

    // 判断是否为初始排序状态
    // 用于赞助商置顶功能：初始状态启用置顶，用户点击排序后失效
    // - 本次会话未手动排序 且 URL 无 sort 参数 → 初始状态
    // - 刷新页面后 hasManualSort 重置为 false，若 URL 无 sort 参数则恢复置顶
    const isInitialSort = !hasManualSort && !hasSortParam;

    // 解析时段过滤参数：空值为 null 表示全天
    const rawTimeFilter = searchParams.get(PARAM_KEYS.timeFilter);
    const timeFilter = rawTimeFilter && rawTimeFilter.trim() ? rawTimeFilter.trim() : null;

    // 解析仅显示收藏参数：'1' 表示启用
    const showFavoritesOnly = searchParams.get(PARAM_KEYS.showFavoritesOnly) === '1';

    // 解析板块参数：允许 hot/secondary/cold/all，其他值回退为 hot
    const rawBoard = searchParams.get(PARAM_KEYS.board);
    const board: BoardFilter = (rawBoard === 'hot' || rawBoard === 'secondary' || rawBoard === 'cold' || rawBoard === 'active' || rawBoard === 'all')
      ? rawBoard
      : DEFAULTS.board;

    return {
      timeRange: searchParams.get(PARAM_KEYS.timeRange) || DEFAULTS.timeRange,
      timeFilter,
      board,
      filterProvider: parseArrayParam(PARAM_KEYS.filterProvider, normalizeLower),
      filterService: parseArrayParam(PARAM_KEYS.filterService, normalizeLower),
      filterChannel: parseArrayParam(PARAM_KEYS.filterChannel, normalizePreserveCase),
      filterCategory: parseArrayParam(PARAM_KEYS.filterCategory, normalizeLower),
      filterVendor: parseArrayParam(PARAM_KEYS.filterVendor, normalizeLower),
      showFavoritesOnly,
      viewMode,
      sortConfig: parseSortParam(rawSortParam),
      isInitialSort,
    };
  }, [searchParams, normalizeLower, normalizePreserveCase, hasManualSort]);

  // 更新单个参数的通用函数
  const updateParam = useCallback((key: string, value: string, defaultValue: string) => {
    commitParams((next) => {
      if (value === defaultValue) {
        next.delete(key);
      } else {
        next.set(key, value);
      }
    });
  }, [commitParams]);

  // 各个状态的 setter
  const setTimeRange = useCallback((value: string) => {
    updateParam(PARAM_KEYS.timeRange, value, DEFAULTS.timeRange);
  }, [updateParam]);

  // 时段过滤 setter（null 表示全天，移除 URL 参数）
  const setTimeFilter = useCallback((value: string | null) => {
    commitParams((next) => {
      if (value === null || value === '') {
        next.delete(PARAM_KEYS.timeFilter);
      } else {
        next.set(PARAM_KEYS.timeFilter, value);
      }
    });
  }, [commitParams]);

  // 板块 setter（默认 hot 时移除参数，保持 URL 简洁）
  const setBoard = useCallback((value: BoardFilter) => {
    updateParam(PARAM_KEYS.board, value, DEFAULTS.board);
  }, [updateParam]);

  // 多选数组参数的通用 setter
  const setArrayParam = useCallback((
    key: string,
    values: string[],
    normalizer: (value: string) => string
  ) => {
    commitParams((next) => {
      // 规范化：去空、去重、排序，使用自定义大小写策略
      const normalized = Array.from(new Set(
        values
          .map(normalizer)
          .filter(v => v && v.toLowerCase() !== 'all')
      )).sort();

      if (normalized.length === 0) {
        // 空数组表示"全部"，移除参数
        next.delete(key);
      } else {
        next.set(key, normalized.join(','));
      }
    });
  }, [commitParams]);

  const setFilterProvider = useCallback((values: string[]) => {
    setArrayParam(PARAM_KEYS.filterProvider, values, normalizeLower);
  }, [setArrayParam, normalizeLower]);

  const setFilterService = useCallback((values: string[]) => {
    setArrayParam(PARAM_KEYS.filterService, values, normalizeLower);
  }, [setArrayParam, normalizeLower]);

  const setFilterChannel = useCallback((values: string[]) => {
    setArrayParam(PARAM_KEYS.filterChannel, values, normalizePreserveCase);
  }, [setArrayParam, normalizePreserveCase]);

  const setFilterCategory = useCallback((values: string[]) => {
    setArrayParam(PARAM_KEYS.filterCategory, values, normalizeLower);
  }, [setArrayParam, normalizeLower]);

  // 厂商 code 是机器标识符（小写受控词表），与 provider/service/category 同款归一
  const setFilterVendor = useCallback((values: string[]) => {
    setArrayParam(PARAM_KEYS.filterVendor, values, normalizeLower);
  }, [setArrayParam, normalizeLower]);

  // 仅显示收藏 setter（true='1'，false=移除参数）
  const setShowFavoritesOnly = useCallback((value: boolean) => {
    commitParams((next) => {
      if (value) {
        next.set(PARAM_KEYS.showFavoritesOnly, '1');
      } else {
        next.delete(PARAM_KEYS.showFavoritesOnly);
      }
    });
  }, [commitParams]);

  const setViewMode = useCallback((value: ViewMode) => {
    updateParam(PARAM_KEYS.viewMode, value, DEFAULTS.viewMode);
  }, [updateParam]);

  const setSortConfig = useCallback((config: SortConfig) => {
    // 标记用户已手动排序（本次会话内置顶失效）
    setHasManualSort(true);
    const serialized = serializeSortConfig(config);
    // 默认排序时移除 URL 参数，刷新后可恢复置顶
    // 非默认排序时保留参数
    commitParams((next) => {
      if (serialized === DEFAULT_SORT_PARAM) {
        next.delete(PARAM_KEYS.sort);
      } else {
        next.set(PARAM_KEYS.sort, serialized);
      }
    });
  }, [commitParams]);

  // 通用：runtime 隐藏/禁用某列后，清掉 URL 里指向该列的旧排序，
  // 避免分享链接刷新时仍触发已隐藏列的排序（不写 hasManualSort，刷新仍可恢复置顶语义）。
  const clearSortForKey = useCallback((targetKey: string) => {
    commitParams((next) => {
      const raw = next.get(PARAM_KEYS.sort);
      if (!raw) return;
      const lastUnderscore = raw.lastIndexOf('_');
      const key = lastUnderscore === -1 ? raw : raw.substring(0, lastUnderscore);
      if (key !== targetKey) return;
      next.delete(PARAM_KEYS.sort);
    });
  }, [commitParams]);

  // runtime 隐藏价格列后清理旧 URL（见 UrlStateActions jsdoc）。
  const clearPriceRatioSort = useCallback(() => clearSortForKey('priceRatio'), [clearSortForKey]);
  // rpdiag 关闭（私有部署）后清理质量列旧 URL 排序。
  const clearQualityScoreSort = useCallback(() => clearSortForKey('qualityScore'), [clearSortForKey]);
  // 厂商列不可见（全站尚未回填 model_vendor）后清理厂商列旧 URL 排序。
  const clearModelVendorSort = useCallback(() => clearSortForKey('modelVendor'), [clearSortForKey]);

  // 进入收藏模式：保存当前筛选状态快照，清空筛选器，启用收藏模式
  const enterFavoritesMode = useCallback(() => {
    // 防止重复进入：已在收藏模式时不重复保存快照（避免覆盖有效快照）
    if (state.showFavoritesOnly) return;

    // 1. 保存当前筛选状态到 sessionStorage
    const snapshot: ListStateSnapshot = {
      version: 1,
      filterProvider: state.filterProvider,
      filterService: state.filterService,
      filterChannel: state.filterChannel,
      filterCategory: state.filterCategory,
      filterVendor: state.filterVendor,
    };
    try {
      sessionStorage.setItem(SNAPSHOT_KEY, JSON.stringify(snapshot));
    } catch {
      // sessionStorage 不可用时静默失败
    }

    // 2. 原子性更新 URL：清空筛选器 + 设置 fav=1
    commitParams((next) => {
      next.delete(PARAM_KEYS.filterProvider);
      next.delete(PARAM_KEYS.filterService);
      next.delete(PARAM_KEYS.filterChannel);
      next.delete(PARAM_KEYS.filterCategory);
      next.delete(PARAM_KEYS.filterVendor);
      next.set(PARAM_KEYS.showFavoritesOnly, '1');
    });
  }, [state.showFavoritesOnly, state.filterProvider, state.filterService, state.filterChannel, state.filterCategory, state.filterVendor, commitParams]);

  // 退出收藏模式：恢复快照中的筛选状态，移除收藏模式标记
  const exitFavoritesMode = useCallback(() => {
    // 1. 尝试从 sessionStorage 恢复快照
    let snapshot: ListStateSnapshot | null = null;
    try {
      snapshot = parseListStateSnapshot(sessionStorage.getItem(SNAPSHOT_KEY));
    } catch {
      // sessionStorage 不可用时使用默认值
    }
    // 无论成功与否都清理快照，避免残留
    try {
      sessionStorage.removeItem(SNAPSHOT_KEY);
    } catch {
      // 静默失败
    }

    // 2. 原子性更新 URL：先清空所有筛选器，再恢复快照中的值
    commitParams((next) => {
      // 移除收藏模式
      next.delete(PARAM_KEYS.showFavoritesOnly);
      // 先清空所有筛选器（避免收藏模式中新增的筛选残留）
      next.delete(PARAM_KEYS.filterProvider);
      next.delete(PARAM_KEYS.filterService);
      next.delete(PARAM_KEYS.filterChannel);
      next.delete(PARAM_KEYS.filterCategory);
      next.delete(PARAM_KEYS.filterVendor);

      // 恢复筛选器（如果快照存在）
      if (snapshot) {
        if (snapshot.filterProvider.length > 0) {
          next.set(PARAM_KEYS.filterProvider, snapshot.filterProvider.join(','));
        }
        if (snapshot.filterService.length > 0) {
          next.set(PARAM_KEYS.filterService, snapshot.filterService.join(','));
        }
        if (snapshot.filterChannel.length > 0) {
          next.set(PARAM_KEYS.filterChannel, snapshot.filterChannel.join(','));
        }
        if (snapshot.filterCategory.length > 0) {
          next.set(PARAM_KEYS.filterCategory, snapshot.filterCategory.join(','));
        }
        if (snapshot.filterVendor.length > 0) {
          next.set(PARAM_KEYS.filterVendor, snapshot.filterVendor.join(','));
        }
      }
      // 无快照时恢复为默认（空数组），即不设置参数
    });
  }, [commitParams]);

  const actions: UrlStateActions = {
    setTimeRange,
    setTimeFilter,
    setBoard,
    setFilterProvider,
    setFilterService,
    setFilterChannel,
    setFilterCategory,
    setFilterVendor,
    setShowFavoritesOnly,
    setViewMode,
    setSortConfig,
    clearPriceRatioSort,
    clearQualityScoreSort,
    clearModelVendorSort,
    enterFavoritesMode,
    exitFavoritesMode,
  };

  return [state, actions];
}

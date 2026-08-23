import { useMemo } from 'react';
import type { TFunction } from 'i18next';
import { vendorLabel } from '../components/VendorBadge';
import type { MultiSelectOption } from '../components/MultiSelect';
import type { ChannelOption, ProcessedMonitorData, ProviderOption } from '../types';

export interface FilterOptionsParams {
  /** 选项基础数据：基于 rawData（未被筛选器过滤），避免筛选器之间循环收敛。 */
  optionsBaseData: ProcessedMonitorData[];
  filterProvider: string[];
  filterService: string[];
  filterChannel: string[];
  /** 分类筛选的**有效值**（运行时隐藏分类筛选器时为空）。 */
  effectiveFilterCategory: string[];
  filterVendor: string[];
  /** i18n 翻译函数，仅厂商选项的本地化 label 需要。 */
  t: TFunction;
}

export interface FilterOptions {
  effectiveProviders: ProviderOption[];
  effectiveServices: string[];
  effectiveChannels: ChannelOption[];
  effectiveCategories: string[];
  effectiveVendors: MultiSelectOption[];
}

/**
 * 五个筛选器的动态选项：每个维度都基于「其他维度已筛选、自身不参与筛选」的数据集
 * 计算，并保证已选项恒可见（无数据时标 `(0)`）。
 */
export function useFilterOptions({
  optionsBaseData,
  filterProvider,
  filterService,
  filterChannel,
  effectiveFilterCategory,
  filterVendor,
  t,
}: FilterOptionsParams): FilterOptions {
  // 动态 Provider 选项：联动筛选 + 保留已选项
  const effectiveProviders = useMemo(() => {
    // 预构建 Set 优化查询性能
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const providerSet = new Set(filterProvider);

    // 1. 应用其他筛选条件（不包括 provider 自身）
    const filtered = optionsBaseData.filter(item => {
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 provider（带计数）
    const availableMap = new Map<string, { label: string; count: number }>();
    filtered.forEach(item => {
      if (!availableMap.has(item.providerId)) {
        availableMap.set(item.providerId, { label: item.providerName, count: 1 });
      } else {
        availableMap.get(item.providerId)!.count++;
      }
    });

    // 3. 确保已选的 provider 始终可见（从全量数据中补充 label）
    filterProvider.forEach(providerId => {
      if (!availableMap.has(providerId)) {
        const item = optionsBaseData.find(d => d.providerId === providerId);
        if (item) {
          availableMap.set(providerId, { label: item.providerName, count: 0 });
        }
      }
    });

    // 4. 转换为选项数组，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[1].label.localeCompare(b[1].label, 'zh-CN'))
      .map(([value, { label, count }]) => ({
        value,
        label: count === 0 && providerSet.has(value) ? `${label} (0)` : label,
      }));
  }, [optionsBaseData, filterService, filterChannel, effectiveFilterCategory, filterVendor, filterProvider]);

  // 动态 Service 选项：联动筛选 + 保留已选项
  const effectiveServices = useMemo(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const serviceSet = new Set(filterService);

    // 1. 应用其他筛选条件（不包括 service 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 service（带计数）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      const service = item.serviceType.toLowerCase();
      availableMap.set(service, (availableMap.get(service) || 0) + 1);
    });

    // 3. 确保已选的 service 始终可见
    filterService.forEach(service => {
      if (!availableMap.has(service)) {
        availableMap.set(service, 0);
      }
    });

    // 4. 转换为数组，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([value, count]) =>
        count === 0 && serviceSet.has(value) ? `${value} (0)` : value
      );
  }, [optionsBaseData, filterProvider, filterChannel, effectiveFilterCategory, filterVendor, filterService]);

  // 动态 Channel 选项：联动筛选 + 保留已选项
  const effectiveChannels = useMemo<ChannelOption[]>(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const channelSet = new Set(filterChannel);

    // 1. 应用其他筛选条件（不包括 channel 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 channel（带计数）+ channelName 映射
    const availableMap = new Map<string, { count: number; label: string }>();
    filtered.forEach(item => {
      if (item.channel) {
        const existing = availableMap.get(item.channel);
        if (existing) {
          existing.count++;
        } else {
          availableMap.set(item.channel, {
            count: 1,
            label: item.channelName || item.channel,
          });
        }
      }
    });

    // 3. 确保已选的 channel 始终可见（从全量数据中查找 channelName）
    filterChannel.forEach(channel => {
      if (!availableMap.has(channel)) {
        // 从全量数据中查找 channelName
        const found = optionsBaseData.find(item => item.channel === channel);
        availableMap.set(channel, {
          count: 0,
          label: found?.channelName || channel,
        });
      }
    });

    // 4. 转换为 ChannelOption[]，按 label 排序，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[1].label.localeCompare(b[1].label, 'zh-CN'))
      .map(([value, { count, label }]) => ({
        value,
        label: count === 0 && channelSet.has(value) ? `${label} (0)` : label,
      }));
  }, [optionsBaseData, filterProvider, filterService, effectiveFilterCategory, filterVendor, filterChannel]);

  // 动态 Category 选项：联动筛选 + 保留已选项
  const effectiveCategories = useMemo(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const categorySet = new Set(effectiveFilterCategory);

    // 1. 应用其他筛选条件（不包括 category 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 category（带计数）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      availableMap.set(item.category, (availableMap.get(item.category) || 0) + 1);
    });

    // 3. 确保已选的 category 始终可见
    effectiveFilterCategory.forEach(category => {
      if (!availableMap.has(category)) {
        availableMap.set(category, 0);
      }
    });

    // 4. 转换为数组，标记无数据的已选项
    return Array.from(availableMap.entries())
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([value, count]) =>
        count === 0 && categorySet.has(value) ? `${value} (0)` : value
      );
  }, [optionsBaseData, filterProvider, filterService, filterChannel, filterVendor, effectiveFilterCategory]);

  // 动态模型厂商选项：联动筛选 + 保留已选项。
  // 与 provider 选项同款是 {value,label} 结构（value=受控 code，label=本地化厂商名），
  // 按 label 排序——下拉里用户读的是名字；表格排序那侧按 code（见 sortMonitors）。
  const effectiveVendors = useMemo<MultiSelectOption[]>(() => {
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = new Set(filterVendor);

    // 1. 应用其他筛选条件（不包括 vendor 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      return true;
    });

    // 2. 收集当前可用的 vendor（带计数）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      if (item.modelVendor) {
        availableMap.set(item.modelVendor, (availableMap.get(item.modelVendor) || 0) + 1);
      }
    });

    // 3. 确保已选的 vendor 始终可见
    filterVendor.forEach(vendor => {
      if (!availableMap.has(vendor)) availableMap.set(vendor, 0);
    });

    // 4. 转换为选项数组（本地化 label），标记无数据的已选项
    return Array.from(availableMap.entries())
      .map(([value, count]) => ({
        value,
        label: count === 0 && vendorSet.has(value) ? `${vendorLabel(t, value)} (0)` : vendorLabel(t, value),
      }))
      .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'));
  }, [optionsBaseData, filterProvider, filterService, filterChannel, effectiveFilterCategory, filterVendor, t]);

  return {
    effectiveProviders,
    effectiveServices,
    effectiveChannels,
    effectiveCategories,
    effectiveVendors,
  };
}

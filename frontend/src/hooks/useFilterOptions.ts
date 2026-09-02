import { useMemo } from 'react';
import type { TFunction } from 'i18next';
import { vendorLabel } from '../components/VendorBadge';
import type { MultiSelectOption } from '../components/MultiSelect';
import type { ChannelOption, ProcessedMonitorData, ProviderOption } from '../types';
import { MODEL_FAMILIES, deriveModelFamily, modelFamilyLabel } from '../utils/modelFamily';
import { collectModelOptions, matchesModelKeys, preferredModelOption } from '../utils/modelFilter';
import type { ModelFilterOption } from '../utils/modelFilter';

/** 家族 code → 展示顺序。下拉分组按此排布，未知家族垫底。 */
const FAMILY_RANK = new Map(MODEL_FAMILIES.map((family, index) => [family.code, index]));
const familyRank = (family: string) => FAMILY_RANK.get(family) ?? MODEL_FAMILIES.length;

export interface FilterOptionsParams {
  /** 选项基础数据：基于 rawData（未被筛选器过滤），避免筛选器之间循环收敛。 */
  optionsBaseData: ProcessedMonitorData[];
  filterProvider: string[];
  filterService: string[];
  filterChannel: string[];
  /** 分类筛选的**有效值**（运行时隐藏分类筛选器时为空）。 */
  effectiveFilterCategory: string[];
  filterVendor: string[];
  /** 模型筛选值：版本级展示名的 canonical key（见 utils/modelFilter）。 */
  filterModel: string[];
  /** i18n 翻译函数，厂商与模型家族的本地化 label 需要。 */
  t: TFunction;
}

export interface FilterOptions {
  effectiveProviders: ProviderOption[];
  effectiveServices: string[];
  effectiveChannels: ChannelOption[];
  effectiveCategories: string[];
  effectiveVendors: MultiSelectOption[];
  effectiveModels: MultiSelectOption[];
}

/**
 * 六个筛选器的动态选项：每个维度都基于「其他维度已筛选、自身不参与筛选」的数据集
 * 计算，并保证已选项恒可见（无数据时标 `(0)`）。
 */
export function useFilterOptions({
  optionsBaseData,
  filterProvider,
  filterService,
  filterChannel,
  effectiveFilterCategory,
  filterVendor,
  filterModel,
  t,
}: FilterOptionsParams): FilterOptions {
  // 动态 Provider 选项：联动筛选 + 保留已选项
  const effectiveProviders = useMemo(() => {
    // 预构建 Set 优化查询性能
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const modelSet = filterModel.length > 0 ? new Set(filterModel) : null;
    const providerSet = new Set(filterProvider);

    // 1. 应用其他筛选条件（不包括 provider 自身）
    const filtered = optionsBaseData.filter(item => {
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      if (modelSet && !matchesModelKeys(item.modelEntries, modelSet)) return false;
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
  }, [optionsBaseData, filterService, filterChannel, effectiveFilterCategory, filterVendor, filterModel, filterProvider]);

  // 动态 Service 选项：联动筛选 + 保留已选项
  const effectiveServices = useMemo(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const modelSet = filterModel.length > 0 ? new Set(filterModel) : null;
    const serviceSet = new Set(filterService);

    // 1. 应用其他筛选条件（不包括 service 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      if (modelSet && !matchesModelKeys(item.modelEntries, modelSet)) return false;
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
  }, [optionsBaseData, filterProvider, filterChannel, effectiveFilterCategory, filterVendor, filterModel, filterService]);

  // 动态 Channel 选项：联动筛选 + 保留已选项
  const effectiveChannels = useMemo<ChannelOption[]>(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const modelSet = filterModel.length > 0 ? new Set(filterModel) : null;
    const channelSet = new Set(filterChannel);

    // 1. 应用其他筛选条件（不包括 channel 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      if (modelSet && !matchesModelKeys(item.modelEntries, modelSet)) return false;
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
  }, [optionsBaseData, filterProvider, filterService, effectiveFilterCategory, filterVendor, filterModel, filterChannel]);

  // 动态 Category 选项：联动筛选 + 保留已选项
  const effectiveCategories = useMemo(() => {
    // 预构建 Set 优化查询性能
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const modelSet = filterModel.length > 0 ? new Set(filterModel) : null;
    const categorySet = new Set(effectiveFilterCategory);

    // 1. 应用其他筛选条件（不包括 category 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      if (modelSet && !matchesModelKeys(item.modelEntries, modelSet)) return false;
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
  }, [optionsBaseData, filterProvider, filterService, filterChannel, filterVendor, filterModel, effectiveFilterCategory]);

  // 动态模型厂商选项：联动筛选 + 保留已选项。
  // 与 provider 选项同款是 {value,label} 结构（value=受控 code，label=本地化厂商名），
  // 按 label 排序——下拉里用户读的是名字；表格排序那侧按 code（见 sortMonitors）。
  const effectiveVendors = useMemo<MultiSelectOption[]>(() => {
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const modelSet = filterModel.length > 0 ? new Set(filterModel) : null;
    const vendorSet = new Set(filterVendor);

    // 1. 应用其他筛选条件（不包括 vendor 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (modelSet && !matchesModelKeys(item.modelEntries, modelSet)) return false;
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
  }, [optionsBaseData, filterProvider, filterService, filterChannel, effectiveFilterCategory, filterModel, filterVendor, t]);

  // 动态模型选项：联动筛选 + 保留已选项 + 按家族分组。
  //
  // 与其它维度的两处不同：
  //   ① 模型是**通道内的多值属性**（一个通道可以同时挂 Opus/Sonnet/Haiku），故计数是
  //      「含该模型的通道数」，各选项计数之和会大于通道总数——这与「选它能看到几行」
  //      的直觉一致，比按 layer 计数有用。
  //   ② label/family 一律取自**未经任何筛选器过滤**的全量数据，而不是当前联动子集：
  //      已选项被联动条件排除后仍要标 `(0)` 保留可见，此时它已不在子集里，只有全量
  //      映射才拿得到它的 family，否则会掉进「其他」组。
  const effectiveModels = useMemo<MultiSelectOption[]>(() => {
    const providerSet = filterProvider.length > 0 ? new Set(filterProvider) : null;
    const serviceSet = filterService.length > 0 ? new Set(filterService) : null;
    const channelSet = filterChannel.length > 0 ? new Set(filterChannel) : null;
    const categorySet = effectiveFilterCategory.length > 0 ? new Set(effectiveFilterCategory) : null;
    const vendorSet = filterVendor.length > 0 ? new Set(filterVendor) : null;
    const modelSet = new Set(filterModel);

    // 0. 全量元数据：key → {label, family}。同一 key 由多条 layer 派生出不同
    //    label/family 时由 preferredModelOption 做确定性裁决，不看遍历顺序。
    //
    //    已知边界：收藏模式下 optionsBaseData 只含收藏项（见 useFilteredData），
    //    此时深链里一个不在收藏集中的已选模型拿不到 metadata，会退回按 key 归族——
    //    对 Gemini 那类 key 里没有厂商前缀的模型意味着落进「其他」组。这与既有
    //    provider/channel 选项在收藏模式下同样只从收藏集补 label 是一致的行为
    //    （provider 甚至是直接不显示该已选项），且只影响分组归属、不影响筛选结果。
    const metadata = new Map<string, ModelFilterOption>();
    optionsBaseData.forEach(item => {
      collectModelOptions(item.modelEntries).forEach(option => {
        const existing = metadata.get(option.key);
        metadata.set(option.key, existing ? preferredModelOption(option, existing) : option);
      });
    });

    // 1. 应用其他筛选条件（不包括 model 自身）
    const filtered = optionsBaseData.filter(item => {
      if (providerSet && !providerSet.has(item.providerId)) return false;
      if (serviceSet && !serviceSet.has(item.serviceType.toLowerCase())) return false;
      if (channelSet && !(item.channel && channelSet.has(item.channel))) return false;
      if (categorySet && !categorySet.has(item.category)) return false;
      if (vendorSet && !(item.modelVendor && vendorSet.has(item.modelVendor))) return false;
      return true;
    });

    // 2. 收集当前可用的 model（计数口径 = 通道数，collectModelOptions 已在通道内去重）
    const availableMap = new Map<string, number>();
    filtered.forEach(item => {
      collectModelOptions(item.modelEntries).forEach(option => {
        availableMap.set(option.key, (availableMap.get(option.key) || 0) + 1);
      });
    });

    // 3. 确保已选的 model 始终可见
    filterModel.forEach(key => {
      if (!availableMap.has(key)) availableMap.set(key, 0);
    });

    // 4. 转换为选项数组：先按家族顺序、组内按 label。**同家族必须连续**——
    //    MultiSelect 按选项数组里的首次出现顺序聚合分组，打散了会分裂成多个同名组。
    return Array.from(availableMap.entries())
      .map(([key, count]) => {
        // 全量映射里都没有的 key（例如用户手改 URL）只能就 key 本身尽力归族。
        const meta = metadata.get(key) ?? { key, label: key, family: deriveModelFamily('', key) };
        return { ...meta, count };
      })
      .sort((a, b) => {
        const byFamily = familyRank(a.family) - familyRank(b.family);
        if (byFamily !== 0) return byFamily;
        return a.label.localeCompare(b.label, 'zh-CN');
      })
      .map(({ key, label, family, count }) => ({
        value: key,
        label: count === 0 && modelSet.has(key) ? `${label} (0)` : label,
        groupKey: family,
        groupLabel: modelFamilyLabel(t, family),
      }));
  }, [optionsBaseData, filterProvider, filterService, filterChannel, effectiveFilterCategory, filterVendor, filterModel, t]);

  return {
    effectiveProviders,
    effectiveServices,
    effectiveChannels,
    effectiveCategories,
    effectiveVendors,
    effectiveModels,
  };
}

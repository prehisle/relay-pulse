import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import i18n from '../i18n';
import { useFilteredData, type FilteredData } from './useFilteredData';
import { useFilterOptions, type FilterOptions } from './useFilterOptions';
import type { ProcessedMonitorData } from '../types';

const mixed: ProcessedMonitorData = {
  id: 'ark-cc-native', providerId: 'ark', providerSlug: 'ark', providerName: 'Ark',
  serviceType: 'cc', serviceName: 'cc', category: 'commercial', sponsor: '', board: 'hot',
  intervalMs: 60000, history: [], currentStatus: 'AVAILABLE', uptime: 100,
  isMultiModel: true, channel: 'native', modelVendors: ['deepseek', 'zhipu'],
  modelEntries: [
    { model: 'DeepSeek', requestModel: 'deepseek-v4-pro', vendor: 'deepseek' },
    { model: 'GLM', requestModel: 'glm-5.3', vendor: 'zhipu' },
  ],
};

function evaluate(vendor: string) {
  let result: { filtered: FilteredData; options: FilterOptions } | undefined;
  const rows = [mixed, { ...mixed, id: 'unknown', modelVendors: undefined }];
  function Probe() {
    const filters = {
      filterProvider: [], filterService: [], filterChannel: [],
      effectiveFilterCategory: [], filterVendor: [vendor], filterModel: [],
    };
    const filtered = useFilteredData({
      ...filters, data: rows, rawData: rows, showFavoritesOnly: true,
      favorites: new Set(rows.map(row => row.id)),
      stats: { total: 2, healthy: 2, issues: 0 },
    });
    const options = useFilterOptions({
      ...filters, optionsBaseData: rows, t: i18n.t.bind(i18n),
    });
    result = { filtered, options };
    return null;
  }
  renderToStaticMarkup(<Probe />);
  return result!;
}

describe('逐层厂商的通道级筛选', () => {
  it.each(['deepseek', 'zhipu'])('选择 %s 保留整行及全部模型层，排除未知厂商', vendor => {
    const { filtered, options } = evaluate(vendor);
    expect(filtered.filteredData).toEqual([mixed]);
    expect(filtered.filteredData[0].modelEntries).toBe(mixed.modelEntries);
    expect(filtered.effectiveStats).toEqual({ total: 1, healthy: 1, issues: 0 });
    expect(options.effectiveServices).toContain('cc');
    expect(options.effectiveChannels.map(option => option.value)).toContain('native');
    expect(options.effectiveVendors.find(option => option.value === vendor)?.label).not.toContain('(0)');
  });

  it('没有匹配厂商时保留已选零计数选项', () => {
    const { filtered, options } = evaluate('moonshot');
    expect(filtered.filteredData).toEqual([]);
    expect(options.effectiveVendors.find(option => option.value === 'moonshot')?.label).toContain('(0)');
  });
});

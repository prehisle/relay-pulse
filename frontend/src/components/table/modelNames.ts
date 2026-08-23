// 模型列的展示派生：桌面表格与移动端卡片共用同一套取名规则，两处渲染形态不同
// （桌面逐行列出、移动端「首个 +N」），故共享的是纯函数而非组件。
import { shortenModelName } from '../../utils/modelName';
import type { ProcessedMonitorData } from '../../types';

export function getModelDisplayList(modelEntries?: ProcessedMonitorData['modelEntries']): string[] {
  if (!modelEntries || modelEntries.length === 0) return [];
  return modelEntries
    .map((entry) => shortenModelName(entry.requestModel) || entry.model || '-')
    .filter(Boolean);
}

export function getModelTooltip(modelEntries?: ProcessedMonitorData['modelEntries']): string | undefined {
  if (!modelEntries || modelEntries.length === 0) return undefined;
  return modelEntries
    .map((entry) => entry.requestModel || entry.model || '-')
    .join('\n');
}

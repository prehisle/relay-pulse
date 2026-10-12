// 模型列的展示派生：桌面表格与移动端卡片共用同一套取名规则，两处渲染形态不同
// （桌面逐行列出、移动端「首个 +N」），故共享的是纯函数而非组件。
import { modelDisplayName } from '../../utils/modelFilter';
import type { ProcessedMonitorData } from '../../types';

/** 展示名派生已下沉到 utils/modelFilter，与「模型」筛选器共用单一真相源——
 *  两处各写一遍表达式，筛选下拉迟早会出现表格里不存在的名字。 */
export function getModelDisplayList(modelEntries?: ProcessedMonitorData['modelEntries']): string[] {
  if (!modelEntries || modelEntries.length === 0) return [];
  return modelEntries
    .map(modelDisplayName)
    .filter(Boolean);
}

/** 完整模型 ID（展示名是缩写，悬浮提示里给出实际请求的那个串）。 */
export function getModelFullNames(modelEntries?: ProcessedMonitorData['modelEntries']): string[] {
  if (!modelEntries) return [];
  return modelEntries.map((entry) => entry.requestModel || entry.model || '-');
}

/** 移动端仍用原生 title（触屏没有悬停），多个 ID 按行拼接。 */
export function getModelTooltip(modelEntries?: ProcessedMonitorData['modelEntries']): string | undefined {
  const names = getModelFullNames(modelEntries);
  return names.length > 0 ? names.join('\n') : undefined;
}

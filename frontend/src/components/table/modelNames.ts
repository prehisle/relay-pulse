// 模型列的展示派生：桌面表格（ModelName 逐行列出）与移动端卡片（「首个 +N」+ 原生 title）
// 共用同一套取名规则，两处渲染形态不同，故共享的是纯函数而非组件。
// 缩写展示名用 utils/modelFilter 的 modelDisplayName——与「模型」筛选器同一个函数，
// 下拉里才不会出现表格里不存在的名字。
import type { ProcessedMonitorData } from '../../types';

type ModelEntry = NonNullable<ProcessedMonitorData['modelEntries']>[number];

/** 完整模型 ID（展示名是缩写，悬浮提示里给出实际请求的那个串）。 */
export function modelFullName(entry: ModelEntry): string {
  return entry.requestModel || entry.model || '-';
}

/** 移动端用原生 title（触屏没有悬停）：每个模型一行，带厂商名时写成「厂商 · 模型 ID」。 */
export function getModelTooltip(
  modelEntries: ProcessedMonitorData['modelEntries'],
  vendorName: (code: string) => string,
): string | undefined {
  if (!modelEntries || modelEntries.length === 0) return undefined;
  return modelEntries
    .map((entry) => (entry.vendor ? `${vendorName(entry.vendor)} · ${modelFullName(entry)}` : modelFullName(entry)))
    .join('\n');
}

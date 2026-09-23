/** 多模型热力图的层间间隙（px） */
export const LAYER_GAP_PX = 2;

/**
 * 单层最小高度（px）。
 *
 * 取值等于 4 层在 20px 表格行里的层高（(20 - 3×2) / 4），所以 4 层及以下的行外观与
 * 固定高度时代完全一致；从第 5 层起才按层数增高，不再把每层压到 2px 以下看不清。
 */
export const MIN_LAYER_HEIGHT_PX = 3.5;

/**
 * 多模型热力图块的总高度：不低于调用方给的基准高度，层数多到放不下时按最小层高撑开。
 */
export function layeredHeatmapHeightPx(layerCount: number, baseHeightPx: number): number {
  if (layerCount <= 0) return baseHeightPx;
  const required = layerCount * MIN_LAYER_HEIGHT_PX + (layerCount - 1) * LAYER_GAP_PX;
  return Math.max(baseHeightPx, required);
}

/** 桌面表格的条件列开关。11 列里这 5 列会按运行时开关或数据情况显隐，
 *  而每一列都要在 <colgroup>、<thead>、每个 <tbody> 行三处各写一遍——三处漏改
 *  一处就整表错位。故由 StatusTable 派生一次、同一个对象喂给表头与行两个组件，
 *  杜绝三处各算各的。守卫见 statusTableColumns.test.tsx（5 轴全组合列数与顺序）。
 *
 *  canonical 顺序（含恒显示列）：
 *    annotation? · provider? · service · channel · model · vendor? · price? ·
 *    listedDays · uptime · lastCheck · quality? · trend
 */
export interface StatusTableColumns {
  /** 标注列。数据驱动：本屏没有任何一行带标注就整列不渲染。 */
  annotation: boolean;
  /** 服务商列。服务商详情页里恒关（页面本身已限定服务商）。 */
  provider: boolean;
  /** 模型厂商列。由调用方基于**未筛选**的全量数据判定，见 StatusTable 的 props 注释。 */
  vendor: boolean;
  /** 价格列。runtime 开关 hide_price_column 的反面。 */
  price: boolean;
  /** 质量列。rpdiag 总开关，私有部署未接 rpdiag 时整列消失。 */
  quality: boolean;
}

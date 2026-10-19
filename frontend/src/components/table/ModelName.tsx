import { useTranslation } from 'react-i18next';
import { getVendorIconComponent, getVendorIconColorClass } from '../VendorIcon';
import { vendorLabel } from '../VendorBadge';
import { modelDisplayName } from '../../utils/modelFilter';
import { modelFullName } from './modelNames';
import type { ProcessedMonitorData } from '../../types';

type ModelEntry = NonNullable<ProcessedMonitorData['modelEntries']>[number];

interface ModelNameProps {
  entry: ModelEntry;
  /** 显示完整模型 ID（悬浮提示里用），默认显示缩写展示名。 */
  full?: boolean;
}

/**
 * 一个模型名，前面是它自己的厂商商标。
 *
 * 厂商是**逐模型**的：同一通道可以混多家（火山方舟一个入口卖 DeepSeek / GLM / 豆包…），
 * 所以图标与名字必须取自同一个 entry——别按下标回查 `item.layers`，modelEntries 会滤掉
 * 空模型层，下标一错位图标就挂到别家模型上。
 *
 * 图标槽恒占位：没声明厂商、或词表外 code（无商标）时留同宽空白，多模型通道的名字才能
 * 左对齐成一列。商标只认得出头部厂商，全名放在 title / aria-label 里。
 */
export function ModelName({ entry, full = false }: ModelNameProps) {
  const { t } = useTranslation();
  const Icon = getVendorIconComponent(entry.vendor);
  const label = Icon && entry.vendor ? vendorLabel(t, entry.vendor) : undefined;

  return (
    <span className="inline-flex items-center gap-1 min-w-0 max-w-full">
      <span
        className="inline-flex w-3.5 h-3.5 shrink-0 items-center justify-center"
        role={label ? 'img' : undefined}
        title={label}
        aria-label={label}
        aria-hidden={label ? undefined : true}
        data-model-vendor={entry.vendor}
      >
        {Icon && <Icon className={`w-3.5 h-3.5 ${getVendorIconColorClass(entry.vendor)}`} />}
      </span>
      <span className="truncate">{full ? modelFullName(entry) : modelDisplayName(entry)}</span>
    </span>
  );
}

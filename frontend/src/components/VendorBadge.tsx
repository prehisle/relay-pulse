import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { getVendorIconComponent, getVendorIconColorClass } from './VendorIcon';

/**
 * 厂商 code → 本地化展示名。
 *
 * 受控词表的真相源在后端（internal/modelvendor），前端只做 code → 文案的映射，
 * 不自建第二份词表；**词表外的 code 原样显示 code**——猜一个名字出来比显示 code 更糟，
 * 那正是「模型是声明的、不是反推的」这条设计红线在展示层的对应物。
 */
export function vendorLabel(t: TFunction, code: string): string {
  return t(`vendors.${code}`, { defaultValue: code });
}

interface VendorBadgeProps {
  /** 通道级厂商 code；为空时整个徽章不渲染（由调用方决定占位符）。 */
  vendor?: string;
  /** 紧凑模式：移动端卡片内与周边 10px 文字同级。 */
  compact?: boolean;
  /** 只出图标不出文字。移动端卡片那一行（通道 / 模型 / 收录天数）宽度本就见底，
   *  多一段厂商名会把通道名和模型名一起挤成省略号——图标占约 12px 就够传达
   *  「不是同一家的模型」，全名留给 title 提示。无图标的未收录 code 仍退回文字，
   *  否则那条通道会彻底没有厂商信号。 */
  iconOnly?: boolean;
  className?: string;
}

/** 厂商徽章：图标 + 本地化厂商名，hover 出「厂商 · code」说明。 */
export function VendorBadge({ vendor, compact = false, iconOnly = false, className = '' }: VendorBadgeProps) {
  const { t } = useTranslation();
  if (!vendor) return null;

  const Icon = getVendorIconComponent(vendor);
  const iconColorClass = getVendorIconColorClass(vendor);
  const label = vendorLabel(t, vendor);
  const showText = !iconOnly || !Icon;

  return (
    <span
      className={`inline-flex items-center gap-1 min-w-0 flex-shrink-0 ${className}`}
      title={t('table.modelVendorTooltip', { vendor: label, code: vendor })}
      aria-label={iconOnly ? label : undefined}
    >
      {/* 品牌色只套在 svg 上、不套在整个徽章上——外层着色会连带把厂商名染成品牌色，
          那是卡片视图既有的文字样式，不该被这次改动波及。 */}
      {Icon && <Icon className={`${compact ? 'w-3 h-3' : 'w-3.5 h-3.5'} flex-shrink-0 ${iconColorClass}`} />}
      {showText && <span className="truncate">{label}</span>}
    </span>
  );
}

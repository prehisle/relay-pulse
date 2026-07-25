import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { ExternalLink as ExternalLinkIcon, AlertTriangle } from 'lucide-react';
import { trackEvent } from '../utils/analytics';
import { ExternalLinkModal } from './ExternalLinkModal';

// sessionStorage key 用于记住"不再提示"选项
const DONT_SHOW_AGAIN_KEY = 'externalLink_dontShowAgain';

/**
 * 只有 http/https 才作为可跳转外链渲染。
 *
 * 这些 URL 来自服务商自助提交，后端已在入口与落盘两处校验协议；此处是纵深防御——
 * 历史脏数据或将来任何绕过后端校验的路径，都不应让 javascript:/data: 之类的伪协议
 * 进到 <a href> 或 window.open（那等于在本站源上执行攻击者代码）。
 * 非法协议降级为纯文本展示，不隐藏内容。
 */
const isSafeExternalHref = (href: string): boolean => {
  try {
    // 刻意不传 base：外链必须是绝对 URL，否则 "javascript" 之外的任意串
    // 都会被解析成同源相对路径而"看起来合法"
    const scheme = new URL(href).protocol;
    return scheme === 'http:' || scheme === 'https:';
  } catch {
    return false;
  }
};

// 检查是否已选择"不再提示"
const shouldSkipConfirm = () => {
  try {
    return sessionStorage.getItem(DONT_SHOW_AGAIN_KEY) === 'true';
  } catch {
    return false;
  }
};

// 保存"不再提示"选项
const saveDontShowAgain = () => {
  try {
    sessionStorage.setItem(DONT_SHOW_AGAIN_KEY, 'true');
  } catch {
    // sessionStorage 不可用时忽略
  }
};

interface ExternalLinkProps {
  href: string | null | undefined;
  children: React.ReactNode;
  className?: string;
  trackLabel?: string;
  compact?: boolean; // 是否使用紧凑模式（32px 高度，用于表格行）
  inline?: boolean; // 纯内联模式（无最小高度），用于多行文本紧凑排版
  requireConfirm?: boolean; // 是否需要二次确认弹窗
}

/**
 * 通用外链组件
 * - 自动添加安全属性 rel="noopener noreferrer"
 * - 显示外链图标
 * - HTTP 链接显示警告图标
 * - 可选二次确认弹窗（用于服务商链接）
 */
export function ExternalLink({
  href,
  children,
  className = '',
  trackLabel,
  compact = false,
  inline = false,
  requireConfirm = false,
}: ExternalLinkProps) {
  const { t } = useTranslation();
  const [showModal, setShowModal] = useState(false);

  // 获取显示名称（用于弹窗和埋点）
  const displayName = typeof children === 'string' ? children : trackLabel || href || '';

  // 记录点击事件
  const trackClick = useCallback(() => {
    if (!href) return;
    const label = trackLabel || (typeof children === 'string' ? children : href);
    let domain: string | undefined;
    try {
      domain = new URL(href).hostname;
    } catch {
      // URL 解析失败时忽略
    }
    trackEvent('click_external_link', {
      link_text: label,
      link_url: href,
      link_domain: domain || '',
      outbound: true,
    });
  }, [trackLabel, children, href]);

  // 执行跳转
  const openLink = useCallback(() => {
    if (!href) return;
    trackClick();
    window.open(href, '_blank', 'noopener,noreferrer');
  }, [href, trackClick]);

  // 点击处理
  const handleClick = useCallback(
    (e: React.MouseEvent) => {
      // 需要确认且未勾选"不再提示"：始终弹窗（安全优先）
      if (requireConfirm && !shouldSkipConfirm()) {
        e.preventDefault();
        setShowModal(true);
        return;
      }

      // 不再提示模式或 requireConfirm=false
      // 修饰键（Cmd/Ctrl/Shift）：保留浏览器原生行为
      if (e.metaKey || e.ctrlKey || e.shiftKey) {
        trackClick();
        return; // 允许 <a> 默认行为
      }

      // 普通点击：用 window.open 避免 GA4 重复收集
      e.preventDefault();
      openLink();
    },
    [requireConfirm, openLink, trackClick]
  );

  // 确认跳转
  const handleConfirm = useCallback(() => {
    setShowModal(false);
    openLink();
  }, [openLink]);

  // 取消
  const handleCancel = useCallback(() => {
    setShowModal(false);
  }, []);

  // 不再提示
  const handleDontShowAgain = useCallback(() => {
    saveDontShowAgain();
  }, []);

  // inline 模式：无最小高度，用于多行文本紧凑排版
  // compact 模式仍保留 32px 最小点击高度（WCAG 建议）
  const sizeClass = inline ? '' : (compact ? 'min-h-[32px] py-0.5 -my-0.5' : 'min-h-[44px] py-1 -my-1');
  const baseClass = `inline-flex items-center gap-1 ${sizeClass} ${className}`.trim();

  // 没有 URL、或协议不在 http/https 白名单内：显示纯文本但保持相同行高，避免表格行高不一致
  if (!href || !isSafeExternalHref(href)) {
    return <span className={baseClass}>{children}</span>;
  }

  const isHttp = href.startsWith('http://');

  // 生成无障碍标签
  const ariaLabel =
    typeof children === 'string'
      ? t('externalLink.ariaLabel', { name: children })
      : t('externalLink.ariaLabelGeneric');

  return (
    <>
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        className={`hover:underline active:underline ${baseClass}`}
        onClick={handleClick}
        aria-label={ariaLabel}
      >
        {children}
        <ExternalLinkIcon size={12} className="flex-shrink-0" aria-hidden="true" />
        {isHttp && (
          <span
            title={t('externalLink.httpWarning')}
            className="inline-flex"
            aria-label={t('externalLink.httpWarning')}
          >
            <AlertTriangle size={12} className="text-yellow-500 flex-shrink-0" aria-hidden="true" />
          </span>
        )}
      </a>

      {requireConfirm && (
        <ExternalLinkModal
          isOpen={showModal}
          targetUrl={href}
          targetName={displayName}
          onConfirm={handleConfirm}
          onCancel={handleCancel}
          onDontShowAgain={handleDontShowAgain}
        />
      )}
    </>
  );
}

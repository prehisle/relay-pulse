import { ExternalLink as ExternalLinkIcon, AlertTriangle } from 'lucide-react';

interface ExternalLinkProps {
  href: string | null | undefined;
  children: React.ReactNode;
  className?: string;
}

/**
 * 通用外链组件
 * - 自动添加安全属性 rel="noopener noreferrer"
 * - 显示外链图标
 * - HTTP 链接显示警告图标
 */
export function ExternalLink({ href, children, className = '' }: ExternalLinkProps) {
  // 如果没有 URL，显示纯文本
  if (!href) {
    return <span className={className}>{children}</span>;
  }

  const isHttp = href.startsWith('http://');

  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center gap-1 hover:underline ${className}`}
    >
      {children}
      <ExternalLinkIcon size={12} className="flex-shrink-0" />
      {isHttp && (
        <span title="非加密 HTTP 链接" className="inline-flex">
          <AlertTriangle
            size={12}
            className="text-yellow-500 flex-shrink-0"
          />
        </span>
      )}
    </a>
  );
}

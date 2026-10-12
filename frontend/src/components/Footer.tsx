import { useState } from 'react';
import { Tag, ChevronDown, ChevronUp, Bug, Zap, Handshake, ShieldCheck, AlertTriangle, ScanSearch } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { GithubIcon } from './icons/GithubIcon';
import { useVersionInfo } from '../hooks/useVersionInfo';
import { FEEDBACK_URLS } from '../constants';

interface FooterProps {
  /** rpdiag 质量功能总开关。false（私有部署未接 rpdiag）时不渲染「中转站检测」外链入口。默认 true。 */
  rpdiagEnabled?: boolean;
}

export function Footer({ rpdiagEnabled = true }: FooterProps) {
  const { t } = useTranslation();
  const { versionInfo } = useVersionInfo();
  const [expanded, setExpanded] = useState(false);

  const notices = [
    {
      icon: Zap,
      title: t('footer.disclaimer.dataReference.title'),
      text: t('footer.disclaimer.dataReference.text'),
    },
    {
      icon: Handshake,
      title: t('footer.disclaimer.neutralMaintenance.title'),
      text: t('footer.disclaimer.neutralMaintenance.text'),
    },
    {
      icon: ShieldCheck,
      title: t('footer.disclaimer.monitoringScope.title'),
      text: t('footer.disclaimer.monitoringScope.text'),
    },
    {
      icon: AlertTriangle,
      title: t('footer.disclaimer.liability.title'),
      text: t('footer.disclaimer.liability.text'),
    },
  ];

  return (
    <footer className="mt-4 bg-surface/60 border border-default rounded-2xl p-4 sm:p-5 text-secondary">
      {/* 免责声明标题 - 移动端可折叠 */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="sm:hidden w-full flex items-center justify-between text-sm font-semibold text-primary mb-2"
      >
        <span>{t('footer.disclaimer.title')}</span>
        {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
      </button>
      <div className="hidden sm:block text-sm font-semibold text-primary mb-3">{t('footer.disclaimer.title')}</div>

      {/* 免责声明内容 - 移动端折叠 */}
      <div className={`${expanded ? 'block' : 'hidden'} sm:block`}>
        <div className="grid gap-2 sm:gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {notices.map(({ icon: Icon, title, text }) => (
            <div
              key={title}
              className="flex items-start gap-2 sm:gap-3 bg-elevated/60 rounded-xl p-2.5 sm:p-3 shadow-sm"
            >
              <div className="text-accent flex-shrink-0 mt-0.5">
                <Icon size={14} className="sm:w-4 sm:h-4" />
              </div>
              <div className="text-[11px] sm:text-xs leading-relaxed">
                <div className="font-semibold text-primary mb-0.5 sm:mb-1">{title}</div>
                <p className="text-muted">{text}</p>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* GitHub 链接与版本信息 */}
      <div className={`${expanded ? 'mt-4 pt-4' : 'mt-2 pt-2 sm:mt-4 sm:pt-4'} border-t border-default/50 flex flex-col sm:flex-row items-center justify-center gap-2 text-xs`}>
        <div className="flex items-center gap-2 flex-wrap justify-center">
          {/* 中转站检测入口 → rpdiag 站点（质量盲评专题内容由 diag 承载，本站旧 /detect 页已下线）。
              rpdiag 未启用时整体不渲染，避免链到无意义站点。 */}
          {rpdiagEnabled && (
            <>
              <a
                href="https://diag.relaypulse.top"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-elevated/50 text-secondary hover:text-accent hover:bg-muted/50 transition min-h-[36px]"
              >
                <ScanSearch size={14} />
                <span>{t('footer.detectLink')}</span>
              </a>
              <span className="hidden sm:inline text-muted">·</span>
            </>
          )}
          <a
            href="https://github.com/prehisle/relay-pulse"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-elevated/50 text-secondary hover:text-accent hover:bg-muted/50 transition min-h-[36px]"
          >
            <GithubIcon size={14} />
            <span>GitHub</span>
          </a>
          <span className="hidden sm:inline text-muted">·</span>
          <a
            href={FEEDBACK_URLS.BUG_REPORT}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-elevated/50 text-secondary hover:text-danger hover:bg-muted/50 transition min-h-[36px]"
          >
            <Bug size={14} />
            <span>{t('footer.issuesBtn')}</span>
          </a>
          <span className="hidden sm:inline text-muted">·</span>
          <span className="text-muted text-[11px] sm:text-xs">{t('footer.openSourceLabel')}</span>
        </div>
        {versionInfo && (
          <>
            <span className="hidden sm:inline text-muted">·</span>
            <div
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-elevated/50 text-secondary"
              title={`Commit: ${versionInfo.git_commit} | Built: ${versionInfo.build_time}`}
            >
              <Tag size={14} className="text-muted" />
              <span className="text-secondary">{versionInfo.version}</span>
            </div>
          </>
        )}
      </div>
    </footer>
  );
}

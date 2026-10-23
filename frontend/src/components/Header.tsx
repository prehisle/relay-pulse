import { useState } from 'react';
import { Activity, CheckCircle, AlertTriangle, Sparkles, Share2, Filter } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useLocation } from 'react-router-dom';

import { SUPPORTED_LANGUAGES, LANGUAGE_PATH_MAP, LANGUAGE_NAMES, isSupportedLanguage, type SupportedLanguage } from '../i18n';
import { FlagIcon } from './FlagIcon';
import { useToast } from './Toast';
import { shareCurrentPage } from '../utils/share';
import { ThemeSwitcher } from './ThemeSwitcher';
import { CommunityMenu } from './CommunityMenu';
import { RefreshButton } from './RefreshButton';

interface HeaderProps {
  stats: {
    total: number;
    healthy: number;
    issues: number;
  };
  // 移动端筛选/刷新相关（可选，用于合并到 Header）
  onFilterClick?: () => void;
  onRefresh?: () => void;
  loading?: boolean;
  refreshCooldown?: boolean;
  autoRefresh?: boolean;
  onToggleAutoRefresh?: () => void;
  activeFiltersCount?: number;
}

export function Header({ stats, onFilterClick, onRefresh, loading, refreshCooldown, autoRefresh = true, onToggleAutoRefresh, activeFiltersCount = 0 }: HeaderProps) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const { showToast } = useToast();

  // 语言下拉菜单状态
  const [showMobileLangMenu, setShowMobileLangMenu] = useState(false);
  const [showDesktopLangMenu, setShowDesktopLangMenu] = useState(false);

  // 获取当前语言，使用类型守卫确保类型安全
  const currentLang: SupportedLanguage = isSupportedLanguage(i18n.language) ? i18n.language : 'zh-CN';

  // 处理分享按钮点击
  const handleShare = async () => {
    const result = await shareCurrentPage();
    if (result.method === 'cancelled') {
      // 用户取消分享，静默处理
      return;
    }
    if (result.success) {
      if (result.method === 'copy') {
        showToast(t('share.linkCopied'), 'success');
      }
      // Web Share API 成功时不需要提示，系统会处理
    } else {
      showToast(t('share.copyFailed'), 'error');
    }
  };

  /**
   * 处理语言切换
   *
   * 逻辑：
   * 1. 移除当前语言的路径前缀（如果有）
   * 2. 添加新语言的路径前缀（中文除外）
   * 3. 保留查询参数和 hash
   * 4. 导航到新路径并更新 i18n 语言状态
   *
   * 示例：
   * - 中文 → 英文：/ → /en/
   * - 英文 → 俄语：/en/docs → /ru/docs
   * - 俄语 → 中文：/ru/docs → /docs
   */
  const handleLanguageChange = (newLang: SupportedLanguage) => {
    // 获取当前语言，使用类型守卫确保类型安全
    const rawLang = i18n.language;
    const currentLang: SupportedLanguage = isSupportedLanguage(rawLang) ? rawLang : 'zh-CN';

    // 构建新路径
    let newPath = location.pathname;
    const queryString = location.search + location.hash;

    // 移除当前语言前缀（如果有）
    const currentPrefix = LANGUAGE_PATH_MAP[currentLang];
    if (currentPrefix && newPath.startsWith(`/${currentPrefix}`)) {
      newPath = newPath.substring(`/${currentPrefix}`.length) || '/';
    }

    // 添加新语言前缀（中文除外）
    const newPrefix = LANGUAGE_PATH_MAP[newLang];
    if (newPrefix) {
      newPath = `/${newPrefix}${newPath === '/' ? '' : newPath}`;
    }

    // 更新 i18n 语言状态
    i18n.changeLanguage(newLang);

    // 导航到新路径
    navigate(newPath + queryString);
  };

  return (
    <header className="flex flex-col gap-1 lg:gap-1.5 mb-2 border-b border-default/50 pb-1.5">
      {/* 第一行：Logo + 标题 + 操作按钮（桌面端右侧完整显示） */}
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 lg:gap-3">
            <div className="p-1.5 lg:p-2 bg-accent/10 rounded-lg border border-accent/20 flex-shrink-0 animate-heartbeat">
              <Activity className="w-5 h-5 lg:w-6 lg:h-6 text-accent" />
            </div>
            <div>
              <h1 className="text-2xl lg:text-3xl font-bold text-gradient-hero">
                RelayPulse
              </h1>
              {/* 桌面端 Tagline - 作为副标题 */}
              <p className="hidden lg:block text-secondary text-xs mt-0.5">
                {t('header.tagline')}
              </p>
            </div>
          </div>
          {/* 移动端 Tagline - 作为副标题 */}
          <p className="lg:hidden text-[10px] text-muted mt-1 pl-1 truncate">
            {t('header.tagline')}
          </p>
        </div>

        {/* 移动端：右上角操作区（语言 + 主题 + 统计卡片） */}
        <div className="flex items-center gap-1 lg:hidden flex-shrink-0">
          {/* 语言切换器 - 点击展开 */}
          <div className="relative">
            <button
              onClick={() => setShowMobileLangMenu(!showMobileLangMenu)}
              className="p-2 rounded-lg bg-elevated/50 hover:bg-muted/50 transition-all duration-200 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              aria-label={t('accessibility.changeLanguage')}
              aria-expanded={showMobileLangMenu}
            >
              <FlagIcon language={currentLang} className="w-5 h-auto" />
            </button>
            {/* 下拉菜单 */}
            {showMobileLangMenu && (
              <>
                {/* 点击外部关闭 */}
                <div
                  className="fixed inset-0 z-40"
                  onClick={() => setShowMobileLangMenu(false)}
                />
                <div
                  className="absolute right-0 mt-1 bg-elevated border border-default rounded-lg shadow-xl z-50"
                  role="listbox"
                  aria-label={t('accessibility.selectLanguage')}
                >
                  {SUPPORTED_LANGUAGES.map((lang) => (
                    <button
                      key={lang}
                      onClick={() => {
                        handleLanguageChange(lang);
                        setShowMobileLangMenu(false);
                      }}
                      className={`w-full p-2 flex items-center justify-center hover:bg-muted/50 transition-colors first:rounded-t-lg last:rounded-b-lg focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                        currentLang === lang ? 'bg-accent/20' : ''
                      }`}
                      role="option"
                      aria-selected={currentLang === lang}
                      aria-label={LANGUAGE_NAMES[lang]?.native || lang}
                    >
                      <FlagIcon language={lang} className="w-5 h-auto flex-shrink-0" />
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>

          {/* 主题切换器 */}
          <ThemeSwitcher />

          {/* 统计卡片 - 极简模式（最右侧） */}
          <div className="flex gap-0.5 ml-0.5 flex-shrink-0">
            <div className="flex items-center gap-0.5 px-1 py-0.5 rounded bg-surface/50 border border-default"
                 title={t('header.stats.healthy')}>
              <CheckCircle size={10} className="text-success" />
              <span className="font-mono font-bold text-success text-[10px]">{stats.healthy}</span>
            </div>
            <div className="flex items-center gap-0.5 px-1 py-0.5 rounded bg-surface/50 border border-default"
                 title={t('header.stats.issues')}>
              <AlertTriangle size={10} className="text-danger" />
              <span className="font-mono font-bold text-danger text-[10px]">{stats.issues}</span>
            </div>
          </div>
        </div>

        {/* 桌面端：右侧完整操作区（语言 + 主题 + 分享 + 推荐 + 统计卡片） */}
        <div className="hidden lg:flex items-center gap-2 flex-shrink-0">
          {/* 语言切换器 - 点击/键盘展开 */}
          <div className="relative inline-block">
            <button
              onClick={() => setShowDesktopLangMenu(!showDesktopLangMenu)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setShowDesktopLangMenu(false);
              }}
              className="p-2 rounded-lg bg-elevated/50 hover:bg-muted/50 transition-all duration-200 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
              aria-label={t('accessibility.changeLanguage')}
              aria-expanded={showDesktopLangMenu}
              aria-haspopup="listbox"
            >
              <FlagIcon language={currentLang} className="w-5 h-auto" />
            </button>
            {showDesktopLangMenu && (
              <>
                {/* 点击外部关闭 */}
                <div
                  className="fixed inset-0 z-40"
                  onClick={() => setShowDesktopLangMenu(false)}
                />
                <div
                  className="absolute left-0 mt-1 bg-elevated border border-default rounded-lg shadow-xl z-50"
                  role="listbox"
                  aria-label={t('accessibility.selectLanguage')}
                >
                  {SUPPORTED_LANGUAGES.map((lang) => (
                    <button
                      key={lang}
                      onClick={() => {
                        handleLanguageChange(lang);
                        setShowDesktopLangMenu(false);
                      }}
                      className={`w-full p-2 flex items-center justify-center hover:bg-muted/50 transition-colors first:rounded-t-lg last:rounded-b-lg focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none ${
                        currentLang === lang ? 'bg-accent/20' : ''
                      }`}
                      role="option"
                      aria-selected={currentLang === lang}
                      aria-label={LANGUAGE_NAMES[lang]?.native || lang}
                    >
                      <FlagIcon language={lang} className="w-5 h-auto flex-shrink-0" />
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>

          {/* 主题切换器 */}
          <ThemeSwitcher />

          {/* 分享按钮 */}
          <button
            onClick={handleShare}
            className="p-2 rounded-lg bg-elevated/50 text-secondary hover:text-primary hover:bg-muted/50 transition-all duration-200 focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
            aria-label={t('share.share')}
            title={t('share.share')}
          >
            <Share2 size={16} />
          </button>

          {/* 加入社群 */}
          <CommunityMenu />

          {/* 联系我们按钮 → 联系页面 */}
          <button
            onClick={() => {
              const langPath = LANGUAGE_PATH_MAP[currentLang];
              navigate(langPath ? `/${langPath}/contact` : '/contact');
            }}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl border border-accent/40 bg-accent/10 text-accent font-semibold tracking-wide shadow-accent hover:bg-accent/20 transition focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
          >
            <Sparkles size={14} />
            {t('header.contactBtn')}
          </button>

          {/* 统计卡片 - 紧凑单行 */}
          <div className="flex gap-2">
            <div className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-surface/50 border border-default"
                 title={t('header.stats.healthy')}>
              <CheckCircle size={14} className="text-success" />
              <span className="font-mono font-bold text-success text-lg">{stats.healthy}</span>
            </div>
            <div className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-surface/50 border border-default"
                 title={t('header.stats.issues')}>
              <AlertTriangle size={14} className="text-danger" />
              <span className="font-mono font-bold text-danger text-lg">{stats.issues}</span>
            </div>
          </div>
        </div>
      </div>

      {/* 移动端：筛选/刷新 + 推荐按钮（960px 以下显示） */}
      <div className="flex items-center gap-1.5 min-[960px]:hidden">
        {/* 移动端：筛选按钮 */}
        {onFilterClick && (
          <button
            onClick={onFilterClick}
            className="flex items-center gap-1 px-2 py-1 bg-elevated text-secondary rounded-lg border border-default hover:bg-muted transition-colors text-xs focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
            title={t('controls.mobile.filterBtn')}
          >
            <Filter size={12} />
            <span>{t('controls.mobile.filterBtnShort')}</span>
            {activeFiltersCount > 0 && (
              <span className="px-1 py-0.5 bg-accent text-inverse text-[10px] rounded-full leading-none">
                {activeFiltersCount}
              </span>
            )}
          </button>
        )}

        {/* 移动端：刷新按钮（状态通过颜色表示，点击=切换+刷新） */}
        {onRefresh && (
          <RefreshButton
            loading={loading || false}
            autoRefresh={autoRefresh}
            refreshCooldown={refreshCooldown || false}
            onRefresh={onRefresh}
            onToggleAutoRefresh={onToggleAutoRefresh}
            size="sm"
            showToggle={false}
          />
        )}

        {/* 分享按钮 - 移动端 */}
        <button
          onClick={handleShare}
          className="flex items-center gap-1 px-2 py-1 rounded-lg bg-elevated/50 text-secondary hover:text-primary hover:bg-muted/50 transition-all duration-200 text-xs ml-auto focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
          aria-label={t('share.share')}
        >
          <Share2 size={12} />
          {t('share.shareShort')}
        </button>

        {/* 加入社群 - 移动端 */}
        <CommunityMenu />

        {/* 联系我们按钮 - 移动端紧凑版 */}
        <button
          onClick={() => {
            const langPath = LANGUAGE_PATH_MAP[currentLang];
            navigate(langPath ? `/${langPath}/contact` : '/contact');
          }}
          className="flex items-center gap-1 px-2 py-1 rounded-lg border border-accent/40 bg-accent/10 text-accent text-xs font-medium shadow-accent hover:bg-accent/20 transition whitespace-nowrap focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none"
        >
          <Sparkles size={12} />
          {t('header.contactBtnShort')}
        </button>
      </div>
    </header>
  );
}

import { useTranslation } from 'react-i18next';

/** 冷板提示条：说明冷板数据不再更新。 */
export function ColdBoardNotice() {
  const { t } = useTranslation();

  return (
    <div className="mb-4 px-4 py-3 bg-info/10 border border-info/30 rounded-lg text-info text-sm flex items-center gap-2">
      <svg className="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{t('controls.boards.coldNotice')}</span>
    </div>
  );
}

import { Server } from 'lucide-react';
import { useTranslation } from 'react-i18next';

/** 加载失败占位。 */
export function ErrorPlaceholder({ message }: { message: string }) {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col items-center justify-center py-20 text-danger">
      <Server size={64} className="mb-4 opacity-20" />
      <p className="text-lg">{t('common.error', { message })}</p>
    </div>
  );
}

/** 首屏加载中占位（仅在还没有任何数据时显示）。 */
export function LoadingPlaceholder() {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col items-center justify-center h-64 text-muted gap-4">
      <div className="w-12 h-12 border-4 border-accent/20 rounded-full animate-spin" style={{ borderTopColor: 'hsl(var(--accent))' }} />
      <p className="animate-pulse">{t('common.loading')}</p>
    </div>
  );
}

/** 后端返回空数据占位。 */
export function NoDataPlaceholder() {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col items-center justify-center py-20 text-muted">
      <Server size={64} className="mb-4 opacity-20" />
      <p className="text-lg">{t('common.noData')}</p>
    </div>
  );
}

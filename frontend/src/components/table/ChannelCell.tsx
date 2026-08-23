import { useTranslation } from 'react-i18next';
import { ChannelTypeIcon, parseChannelType } from '../ChannelTypeIcon';
import { HoverTooltip } from '../HoverTooltip';

// 通道单元格组件（带自定义 CSS tooltip，替代原生 title 属性）
interface ChannelCellProps {
  channel?: string;
  probeUrl?: string;
  templateName?: string;
  coldReason?: string;
  boardReason?: string;
  boardReasonModels?: string;
  className?: string;
}

export function ChannelCell({ channel, probeUrl, templateName, coldReason, boardReason, boardReasonModels, className = '' }: ChannelCellProps) {
  const { t } = useTranslation();
  const channelType = parseChannelType(channel);
  const isQualityHardFail = boardReason === 'quality_hardfail';
  const hasTooltip = !!(channelType || probeUrl || templateName || coldReason || isQualityHardFail);

  const channelContent = (
    <>
      <ChannelTypeIcon channel={channel} />
      <span className="min-w-0 truncate">{channel || '-'}</span>
    </>
  );

  if (!hasTooltip) {
    return <span className={`inline-flex items-center gap-1 ${className}`}>{channelContent}</span>;
  }

  return (
    <HoverTooltip
      triggerClassName={`gap-1 cursor-help ${className}`}
      content={
        <span className="flex flex-col gap-1">
          {channelType && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.channelType')}</span>
              <span className="text-primary text-[11px]">
                {t(`table.channelType.${channelType}`)} — {t(`table.channelType.${channelType}Desc`)}
              </span>
            </span>
          )}
          {probeUrl && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.probeUrl')}</span>
              <span className="text-primary font-mono text-[11px] break-all">{probeUrl}</span>
            </span>
          )}
          {templateName && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.template')}</span>
              <span className="text-primary font-mono text-[11px] break-all">{templateName}</span>
            </span>
          )}
          {coldReason && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.coldReason', '冷板原因')}</span>
              <span className="text-warning text-[11px] break-all">{coldReason}</span>
            </span>
          )}
          {isQualityHardFail && (
            <span className="flex flex-col">
              <span className="text-muted text-[10px]">{t('table.channelTooltip.qualityHardFail.label', '质量移板')}</span>
              <span className="text-warning text-[11px] break-all">
                {boardReasonModels
                  ? t('table.channelTooltip.qualityHardFail.text', '{{models}} 近3次评测均未取得可评分响应，已暂移备用板', { models: boardReasonModels })
                  : t('table.channelTooltip.qualityHardFail.textNoModels', '近3次评测均未取得可评分响应，已暂移备用板')}
              </span>
            </span>
          )}
        </span>
      }
    >
      {channelContent}
    </HoverTooltip>
  );
}

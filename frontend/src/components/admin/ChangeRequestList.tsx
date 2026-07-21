import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Loader2,
  AlertCircle,
  ChevronDown,
  ChevronUp,
  Check,
  X,
  Play,
  Trash2,
  Save,
  ArrowRight,
} from 'lucide-react';
import type { AdminChangeRequest, ChangeRequestStatus } from '../../types/change';
import { fieldShapeClass } from './fieldStyles';
import { FormField, ReadOnlyField } from './FormControls';

// ── JSON 解析工具 ────────────────────────────────────────

function parseJsonRecord(json: string | undefined): Record<string, string> {
  if (!json) return {};
  try {
    const parsed = JSON.parse(json) as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed).map(([k, v]) => [k, v == null ? '' : String(v)]),
    );
  } catch {
    return {};
  }
}

// ── AdminNoteEditor 子组件 ───────────────────────────────

interface AdminNoteEditorProps {
  cr: AdminChangeRequest;
  onUpdate: (id: string, updates: Record<string, unknown>) => void;
}

/**
 * 审核备注编辑器：
 *   - status 非 'applied' → 可编辑（FormField），有改动时显示保存按钮
 *   - status === 'applied' → 只读展示（ReadOnlyField）
 * 父组件重新拉取数据后以新的 key（`${public_id}-${admin_note}`）重挂载，自动重置本地草稿。
 * 见调用处 key 设置。
 */
function AdminNoteEditor({ cr, onUpdate }: AdminNoteEditorProps) {
  const { t } = useTranslation();
  const sourceNote = cr.admin_note ?? '';
  const [note, setNote] = useState(sourceNote);
  const dirty = note !== sourceNote;

  if (cr.status === 'applied') {
    return (
      <div className="rounded-lg border border-default/60 bg-elevated/20 p-3">
        <ReadOnlyField label={t('admin.changes.adminNote')} value={sourceNote} />
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-default/60 bg-elevated/20 p-3 space-y-2">
      <FormField
        label={t('admin.changes.adminNote')}
        value={note}
        onChange={setNote}
        placeholder={t('admin.changes.adminNotePlaceholder')}
        multiline
      />
      {dirty && (
        <div className="flex justify-end">
          <button
            onClick={() => onUpdate(cr.public_id, { admin_note: note })}
            className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-accent/10 text-accent hover:bg-accent/20 transition"
          >
            <Save size={11} />
            {t('common.save', { defaultValue: '保存' })}
          </button>
        </div>
      )}
    </div>
  );
}

// ── ChangeRequestList 主组件 ─────────────────────────────

interface ChangeRequestListProps {
  changes: AdminChangeRequest[];
  isLoading: boolean;
  statusFilter: ChangeRequestStatus | 'all';
  setStatusFilter: (f: ChangeRequestStatus | 'all') => void;
  onUpdate: (id: string, updates: Record<string, unknown>) => void;
  onApprove: (id: string) => void;
  onReject: (id: string, note: string) => void;
  onApply: (id: string) => void;
  onDelete: (id: string) => void;
  pendingActions?: Record<string, string>;
  error: string | null;
  featureDisabled?: boolean;
}

const STATUS_FILTERS: (ChangeRequestStatus | 'all')[] = ['all', 'pending', 'approved', 'rejected', 'applied'];

export function ChangeRequestList({
  changes,
  isLoading,
  statusFilter,
  setStatusFilter,
  onUpdate,
  onApprove,
  onReject,
  onApply,
  onDelete,
  pendingActions,
  error,
  featureDisabled,
}: ChangeRequestListProps) {
  const { t } = useTranslation();
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [rejectNote, setRejectNote] = useState('');
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  // 字段键 → 本地化标签；未收录的键回退原始键名，避免漏译时出现空白
  const fieldLabel = (key: string) => t(`admin.changes.fields.${key}`, { defaultValue: key });

  const statusLabel = (status: string) => {
    const map: Record<string, string> = {
      pending: t('admin.changes.statusPending'),
      approved: t('admin.changes.statusApproved'),
      rejected: t('admin.changes.statusRejected'),
      applied: t('admin.changes.statusApplied'),
    };
    return map[status] || status;
  };

  const statusColor = (status: string) => {
    switch (status) {
      case 'pending': return 'text-warning';
      case 'approved': return 'text-accent';
      case 'rejected': return 'text-danger';
      case 'applied': return 'text-success';
      default: return 'text-muted';
    }
  };

  if (featureDisabled) {
    return (
      <div className="p-4 bg-muted/10 border border-default rounded-lg text-muted text-sm">
        {t('admin.changes.featureDisabled')}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Status filter */}
      <div className="flex gap-1 flex-wrap">
        {STATUS_FILTERS.map(f => (
          <button
            key={f}
            onClick={() => setStatusFilter(f)}
            className={`px-3 py-1.5 text-xs rounded-lg transition ${
              statusFilter === f
                ? 'bg-accent/20 text-accent font-medium'
                : 'bg-elevated text-muted hover:text-secondary'
            }`}
          >
            {f === 'all' ? t('admin.filter.all') : statusLabel(f)}
          </button>
        ))}
      </div>

      {error && (
        <div className="flex items-center gap-2 p-3 rounded-lg bg-danger/10 text-danger text-sm">
          <AlertCircle size={16} />
          <span>{error}</span>
        </div>
      )}

      {isLoading ? (
        <div className="flex items-center justify-center py-8 text-muted">
          <Loader2 size={20} className="animate-spin mr-2" />
          {t('admin.table.loading')}
        </div>
      ) : changes.length === 0 ? (
        <div className="text-center py-8 text-muted">{t('admin.changes.empty')}</div>
      ) : (
        <div className="space-y-2">
          {changes.map(cr => {
            const isExpanded = expandedId === cr.public_id;
            const proposed = parseJsonRecord(cr.proposed_changes);
            const snapshot = parseJsonRecord(cr.current_snapshot);
            const liveCurrent = cr.live_current ?? {};

            return (
              <div key={cr.public_id} className="rounded-xl border border-default bg-surface overflow-hidden">
                {/* Row header */}
                <button
                  onClick={() => setExpandedId(isExpanded ? null : cr.public_id)}
                  className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-elevated/50 transition"
                >
                  <code className="text-xs text-muted font-mono">{cr.public_id.slice(0, 8)}</code>
                  <span className="text-sm text-primary font-medium flex-1 truncate">{cr.target_key}</span>
                  <span className={`text-xs px-2 py-0.5 rounded-md bg-muted/20 ${statusColor(cr.status)}`}>
                    {statusLabel(cr.status)}
                  </span>
                  <span className="text-xs text-muted">
                    {cr.apply_mode === 'auto' ? t('admin.changes.modeAuto') : t('admin.changes.modeManual')}
                  </span>
                  <span className="text-xs text-muted">
                    {new Date(cr.created_at * 1000).toLocaleDateString()}
                  </span>
                  {isExpanded ? <ChevronUp size={14} className="text-muted" /> : <ChevronDown size={14} className="text-muted" />}
                </button>

                {/* Expanded detail */}
                {isExpanded && (
                  <div className="px-4 pb-4 border-t border-default/50 space-y-3">
                    {/* 只读 diff：当前值 → 提议值 */}
                    <div className="mt-3">
                      <div className="text-xs font-medium text-muted mb-1">{t('admin.changes.proposedChanges')}</div>
                      {cr.live_current_source && cr.live_current_source !== 'auto' && (
                        <div className="text-[11px] text-warning mb-1">
                          {t(`admin.changes.liveUnavailable.${cr.live_current_source}`)}
                        </div>
                      )}
                      <div className="space-y-1">
                        {/* 列头 */}
                        <div className="grid grid-cols-[110px_1fr_auto_1fr] gap-2 items-center text-[11px] font-medium uppercase tracking-wide text-muted/70">
                          <span>{t('admin.changes.colField')}</span>
                          <span>{t('admin.changes.colCurrent')}</span>
                          <span aria-hidden="true" />
                          <span>{t('admin.changes.colProposed')}</span>
                        </div>
                        {/* 逐字段只读 diff */}
                        {Object.entries(proposed).map(([k, v]) => {
                          // 当前值（"现网真实值"）取值策略：
                          //   auto            → live_current 优先、缺失回退提交快照
                          //   manual          → 提交快照（人工通道无实时配置，快照是合法参照，warning 已说明）
                          //   deleted / error → 不显示（''→'—'）：通道已删/读失败时没有可信"当前值"，
                          //                     展示陈旧快照会误导审批方对错误基线放行（warning 已说明原因）
                          //   其他/未知        → 提交快照（防御性向后兼容）
                          const src = cr.live_current_source;
                          const current = (
                            src === 'auto'   ? (liveCurrent[k] ?? snapshot[k]) :
                            src === 'manual' ? snapshot[k] :
                            src === 'deleted' || src === 'error' ? '' :
                            snapshot[k]
                          ) ?? '';
                          return (
                            <div key={k} className="grid grid-cols-[110px_1fr_auto_1fr] gap-2 items-center text-sm">
                              <span className="text-xs text-muted truncate" title={k}>{fieldLabel(k)}</span>
                              <span className="text-xs text-secondary truncate" title={current}>{current || '—'}</span>
                              <ArrowRight className="w-3 h-3 flex-shrink-0 text-muted/50" aria-hidden="true" />
                              <span className="text-xs text-primary font-medium truncate" title={v}>{v || '—'}</span>
                            </div>
                          );
                        })}
                      </div>
                      <p className="mt-2 text-[11px] text-muted">{t('admin.changes.editElsewhereHint')}</p>
                    </div>

                    {/* 审核备注（可编辑 / 只读）。key 确保父级 refetch 后以新备注重置本地草稿 */}
                    <AdminNoteEditor
                      key={`${cr.public_id}-${cr.admin_note ?? ''}`}
                      cr={cr}
                      onUpdate={onUpdate}
                    />

                    {/* New API Key indicator */}
                    {cr.new_key_last4 && (
                      <div className="text-sm">
                        <span className="text-muted">{t('admin.changes.newApiKey')}:</span>{' '}
                        <span className="text-primary">...{cr.new_key_last4}</span>
                      </div>
                    )}

                    {/* Test info */}
                    {cr.requires_test && (
                      <div className="space-y-1 text-xs text-muted">
                        {cr.test_type && (
                          <div>{t('admin.changes.testType', { defaultValue: '服务类型' })}: {cr.test_type}</div>
                        )}
                        {cr.test_variant && (
                          <div>{t('admin.changes.testVariant', { defaultValue: '请求模板' })}: {cr.test_variant}</div>
                        )}
                        {cr.test_passed_at && (
                          <div>
                            {t('admin.changes.testInfo')}: {cr.test_latency_ms}ms / HTTP {cr.test_http_code}
                          </div>
                        )}
                      </div>
                    )}

                    {/* 反作弊 attestation 三态审计行 */}
                    <div className="text-xs">
                      {!cr.requires_test ? (
                        <span className="text-muted">{t('admin.changes.antiCheatNA')}</span>
                      ) : cr.agreement_accepted ? (
                        <span className="text-success">
                          {t('admin.changes.antiCheatConfirmed')}
                          {cr.agreement_version ? ` · v${cr.agreement_version}` : ''}
                          {cr.agreement_accepted_at
                            ? ` · ${new Date(cr.agreement_accepted_at * 1000).toLocaleString()}`
                            : ''}
                        </span>
                      ) : (
                        <span className="text-danger font-medium">⚠ {t('admin.changes.antiCheatMissing')}</span>
                      )}
                    </div>

                    {/* Actions */}
                    <div className="space-y-2 pt-2">
                      <div className="flex gap-2 flex-wrap">
                        {cr.status === 'pending' && (
                          <>
                            <button
                              onClick={() => onApprove(cr.public_id)}
                              disabled={!!pendingActions?.[cr.public_id]}
                              className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-accent/10 text-accent hover:bg-accent/20 transition disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                              {pendingActions?.[cr.public_id] === 'approve'
                                ? <Loader2 size={12} className="animate-spin" />
                                : <Check size={12} />}
                              {t('admin.changes.approve')}
                            </button>
                            <div className="flex items-center gap-1">
                              <input
                                type="text"
                                value={rejectNote}
                                onChange={e => setRejectNote(e.target.value)}
                                placeholder={t('admin.changes.rejectNotePlaceholder')}
                                className={`${fieldShapeClass({ dense: true, xs: true })} w-48`}
                              />
                              <button
                                onClick={() => { onReject(cr.public_id, rejectNote); setRejectNote(''); }}
                                disabled={!!pendingActions?.[cr.public_id]}
                                className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-danger/10 text-danger hover:bg-danger/20 transition disabled:opacity-50 disabled:cursor-not-allowed"
                              >
                                {pendingActions?.[cr.public_id] === 'reject'
                                  ? <Loader2 size={12} className="animate-spin" />
                                  : <X size={12} />}
                                {t('admin.changes.reject')}
                              </button>
                            </div>
                          </>
                        )}
                        {cr.status === 'approved' && cr.apply_mode === 'auto' && (
                          <button
                            onClick={() => onApply(cr.public_id)}
                            disabled={!!pendingActions?.[cr.public_id]}
                            className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-success/10 text-success hover:bg-success/20 transition disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            {pendingActions?.[cr.public_id] === 'apply'
                              ? <Loader2 size={12} className="animate-spin" />
                              : <Play size={12} />}
                            {t('admin.changes.apply')}
                          </button>
                        )}
                        {confirmDeleteId === cr.public_id ? (
                          <div className="flex items-center gap-1">
                            <span className="text-xs text-danger">{t('admin.changes.confirmDelete')}</span>
                            <button
                              onClick={() => { onDelete(cr.public_id); setConfirmDeleteId(null); }}
                              className="px-2 py-1 text-xs rounded bg-danger text-white"
                            >
                              {t('admin.changes.delete')}
                            </button>
                            <button
                              onClick={() => setConfirmDeleteId(null)}
                              className="px-2 py-1 text-xs rounded border border-default text-muted"
                            >
                              {t('common.cancel')}
                            </button>
                          </div>
                        ) : (
                          <button
                            onClick={() => setConfirmDeleteId(cr.public_id)}
                            disabled={!!pendingActions?.[cr.public_id]}
                            className="flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg text-muted hover:text-danger transition ml-auto disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            {pendingActions?.[cr.public_id] === 'delete'
                              ? <Loader2 size={12} className="animate-spin" />
                              : <Trash2 size={12} />}
                            {t('admin.changes.delete')}
                          </button>
                        )}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

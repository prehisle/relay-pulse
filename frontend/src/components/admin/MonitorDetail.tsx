import { useEffect, useState, type InputHTMLAttributes } from 'react';
import { useTranslation } from 'react-i18next';
import { Eye, EyeOff, Copy, Check, X } from 'lucide-react';
import type { MonitorConfig, MonitorFile, ProbeHistoryEntry, ProbeTarget } from '../../types/monitor';
import type { ProbeResult } from '../../hooks/useMonitorAdmin';
import { PARENT_TARGET_KEY } from '../../hooks/useMonitorAdmin';
import { MonitorLogsTab } from './MonitorLogsTab';
import { CurlCommandBlock } from './CurlCommandBlock';
import { fieldInputClass, fieldShapeClass } from './fieldStyles';
import { buildVendorOptions, useModelVendors } from '../../hooks/useModelVendors';

type DetailTab = 'detail' | 'logs';

interface MonitorDetailProps {
  fetchTemplates: () => Promise<string[]>;
  monitorFile: MonitorFile;
  monitorKey: string;
  onBack: () => void;
  onSave: (file: MonitorFile, revision: number) => Promise<void>;
  onDelete: () => void;
  onToggle: (field: 'disabled' | 'hidden', value: boolean) => void;
  onProbe: (
    overrides?: { template?: string; base_url?: string; api_key?: string },
    targetModel?: string,
  ) => Promise<ProbeResult | null>;
  fetchLogs: (
    key: string,
    opts?: { since?: string; limit?: number; model?: string },
  ) => Promise<ProbeHistoryEntry[]>;
  /** 可探测目标（父 + 各子通道），来自详情接口的 probe_targets。 */
  probeTargets?: ProbeTarget[];
  /** 按 target key（model，父通道用 PARENT_TARGET_KEY）分桶的探测态。 */
  probingTargets?: Record<string, boolean>;
  probeResults?: Record<string, ProbeResult>;
  probeErrors?: Record<string, string>;
}

type EditableFields = Pick<MonitorConfig,
  'provider_name' | 'channel_name' | 'provider_url' | 'template' | 'base_url' | 'api_key' | 'proxy' |
  'category' | 'sponsor_level' | 'board' | 'interval' | 'listed_since' | 'expires_at' |
  'price_min' | 'price_max' | 'key_type' | 'auto_cold_exempt' | 'auto_move_exempt'
>;

interface ChildEdit {
  _original?: MonitorConfig;
  model: string;
  /** 模型厂商 code：套 native 模板的子行必须填，漏填会经 config > template 回退链继承成错误厂商 */
  model_vendor: string;
  template: string;
  base_url: string;
  api_key: string;
}

interface SelectOption {
  value: string;
  label: string;
}

export function MonitorDetail({
  fetchTemplates, monitorFile, monitorKey, onBack,
  onSave, onDelete, onToggle, onProbe, fetchLogs,
  probeTargets = [], probingTargets = {}, probeResults = {}, probeErrors = {},
}: MonitorDetailProps) {
  const { t } = useTranslation();
  const vendors = useModelVendors();

  /** 厂商下拉选项：空值在首位，当前值即使不在词表也保留（免得一保存就把历史值清空） */
  const vendorOptions = (current: string) =>
    buildVendorOptions(vendors, current, t('admin.monitors.sponsorLevels.none'));
  const [activeTab, setActiveTab] = useState<DetailTab>('detail');
  const [isEditing, setIsEditing] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [templates, setTemplates] = useState<string[]>([]);

  const root = monitorFile.monitors.find(m => !m.parent) || monitorFile.monitors[0];
  const children = monitorFile.monitors.filter(m => m.parent);
  // 子通道测试行直接由后端 resolved 的 child 目标驱动（不和 raw children 按下标配对），
  // 这样 model 标签与探测请求 target_model 完全同源，避免 raw/runtime 集合错位导致串行。
  const childTargets = probeTargets.filter(t => t.role === 'child');

  // 父通道探测态（按钮禁用 / 结果展示均读这一桶）。
  const parentProbing = !!probingTargets[PARENT_TARGET_KEY];
  const parentResult = probeResults[PARENT_TARGET_KEY];
  const parentError = probeErrors[PARENT_TARGET_KEY];

  const [editFields, setEditFields] = useState<EditableFields>({
    provider_name: root?.provider_name || '',
    channel_name: root?.channel_name || '',
    provider_url: root?.provider_url || '',
    template: root?.template || '',
    base_url: root?.base_url || '',
    api_key: root?.api_key || '',
    proxy: root?.proxy || '',
    category: root?.category || '',
    sponsor_level: root?.sponsor_level || '',
    board: root?.board || 'hot',
    interval: root?.interval || '',
    listed_since: root?.listed_since || '',
    expires_at: root?.expires_at || '',
    price_min: root?.price_min ?? null,
    price_max: root?.price_max ?? null,
    key_type: root?.key_type || '',
    auto_cold_exempt: root?.auto_cold_exempt ?? false,
    auto_move_exempt: root?.auto_move_exempt ?? false,
  });

  const [editChildren, setEditChildren] = useState<ChildEdit[]>([]);

  // 价格字段使用独立 raw string 状态，避免 parseFloat("0.") 丢失小数点的问题
  const [priceMinRaw, setPriceMinRaw] = useState('');
  const [priceMaxRaw, setPriceMaxRaw] = useState('');
  const [priceError, setPriceError] = useState('');

  useEffect(() => {
    let active = true;
    fetchTemplates()
      .then(items => { if (active) setTemplates(items); })
      .catch(() => { if (active) setTemplates([]); });
    return () => { active = false; };
  }, [fetchTemplates]);

  const templateOptions = withCurrentOption(
    [
      { value: '', label: t('admin.monitors.templateNone') },
      ...Array.from(new Set(templates)).sort().map(name => ({ value: name, label: name })),
    ],
    isEditing ? editFields.template : root?.template,
  );

  const categoryOptions = withCurrentOption([
    { value: 'commercial', label: t('admin.monitors.categoryCommercial') },
    { value: 'public', label: t('admin.monitors.categoryPublic') },
  ], isEditing ? editFields.category : root?.category);

  const sponsorLevelOptions = withCurrentOption([
    { value: '', label: t('admin.monitors.sponsorLevels.none') },
    { value: 'public', label: t('admin.monitors.sponsorLevels.public') },
    { value: 'signal', label: t('admin.monitors.sponsorLevels.signal') },
    { value: 'pulse', label: t('admin.monitors.sponsorLevels.pulse') },
    { value: 'beacon', label: t('admin.monitors.sponsorLevels.beacon') },
    { value: 'backbone', label: t('admin.monitors.sponsorLevels.backbone') },
    { value: 'core', label: t('admin.monitors.sponsorLevels.core') },
  ], isEditing ? editFields.sponsor_level : root?.sponsor_level);

  const boardOptions = withCurrentOption([
    { value: 'hot', label: t('admin.monitors.boardHot') },
    { value: 'secondary', label: t('admin.monitors.boardSecondary') },
    { value: 'cold', label: t('admin.monitors.boardCold') },
  ], isEditing ? editFields.board : (root?.board || 'hot'));

  const toChildEdits = (items: MonitorConfig[]): ChildEdit[] =>
    items.map(c => ({
      _original: c,
      model: c.model || '',
      model_vendor: c.model_vendor || '',
      template: c.template || '',
      base_url: c.base_url || '',
      api_key: c.api_key || '',
    }));

  const startEditing = () => {
    setEditFields({
      provider_name: root?.provider_name || '',
      channel_name: root?.channel_name || '',
      provider_url: root?.provider_url || '',
      template: root?.template || '',
      base_url: root?.base_url || '',
      api_key: root?.api_key || '',
      proxy: root?.proxy || '',
      category: root?.category || '',
      sponsor_level: root?.sponsor_level || '',
      board: root?.board || 'hot',
      interval: root?.interval || '',
      listed_since: root?.listed_since || '',
      expires_at: root?.expires_at || '',
      price_min: root?.price_min ?? null,
      price_max: root?.price_max ?? null,
      key_type: root?.key_type || '',
      auto_cold_exempt: root?.auto_cold_exempt ?? false,
      auto_move_exempt: root?.auto_move_exempt ?? false,
    });
    setEditChildren(toChildEdits(children));
    setPriceMinRaw(root?.price_min != null ? String(root.price_min) : '');
    setPriceMaxRaw(root?.price_max != null ? String(root.price_max) : '');
    setPriceError('');
    setSaveError(null);
    setIsEditing(true);
  };

  const cancelEditing = () => {
    setIsEditing(false);
    setSaveError(null);
  };

  const addChild = () => {
    setEditChildren(prev => [...prev, { model: '', model_vendor: '', template: '', base_url: '', api_key: '' }]);
  };

  const removeChild = (index: number) => {
    setEditChildren(prev => prev.filter((_, i) => i !== index));
  };

  const updateChild = (index: number, field: keyof ChildEdit, value: string) => {
    setEditChildren(prev => prev.map((c, i) => i === index ? { ...c, [field]: value } : c));
  };

  const parsePriceRaw = (raw: string): number | null => {
    if (raw.trim() === '' || raw.trim() === '.') return null;
    const n = parseFloat(raw);
    return isNaN(n) || n < 0 ? null : n;
  };

  const handleSave = async () => {
    // 价格校验
    const minOk = priceMinRaw === '' || /^\d+\.?\d*$/.test(priceMinRaw.trim());
    const maxOk = priceMaxRaw === '' || /^\d+\.?\d*$/.test(priceMaxRaw.trim());
    if (!minOk || !maxOk) {
      setPriceError('价格须为非负数（如 0.5、1、3.14）');
      return;
    }
    setPriceError('');

    setIsSaving(true);
    setSaveError(null);
    try {
      const parentPath = `${root.provider}/${root.service}/${root.channel}`;
      const updatedRoot = {
        ...root,
        ...editFields,
        price_min: parsePriceRaw(priceMinRaw),
        price_max: parsePriceRaw(priceMaxRaw),
      };
      const updatedChildren: MonitorConfig[] = editChildren.map(c => ({
        ...c._original,
        provider: c._original?.provider || '',
        service: c._original?.service || '',
        channel: c._original?.channel || '',
        parent: parentPath,
        model: c.model.trim() || undefined,
        model_vendor: c.model_vendor.trim() || undefined,
        template: c.template || undefined,
        base_url: c.base_url || undefined,
        api_key: c.api_key || undefined,
      }));
      const updatedFile: MonitorFile = {
        ...monitorFile,
        monitors: [updatedRoot, ...updatedChildren],
      };
      await onSave(updatedFile, monitorFile.metadata.revision);
      setIsEditing(false);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e));
    } finally {
      setIsSaving(false);
    }
  };

  // 模板被改动后，禁用 Probe（后端会拒绝 template override，因为模板字段变化
  // 涉及 URLPattern/Headers/Body 的重新解析，沙箱无法做"半解析"）。
  const templateDirty = isEditing && (editFields.template || '') !== (root?.template || '');

  const handleProbe = async () => {
    // 编辑模式下用 draft 字段覆盖磁盘配置探测；template 不再发送（后端拒绝 template 覆盖）。
    if (isEditing) {
      await onProbe({
        base_url: editFields.base_url,
        api_key: editFields.api_key,
      }, PARENT_TARGET_KEY);
    } else {
      await onProbe(undefined, PARENT_TARGET_KEY);
    }
  };

  // 子通道只测已保存的 runtime resolved 配置，不传 base_url/api_key 草稿覆盖。
  const handleChildProbe = (targetModel: string) => onProbe(undefined, targetModel);

  const handleDelete = async () => {
    setIsDeleting(true);
    try {
      onDelete();
    } finally {
      setIsDeleting(false);
      setShowDeleteConfirm(false);
    }
  };

  const updateField = <K extends keyof EditableFields>(key: K, value: EditableFields[K]) => {
    setEditFields(prev => ({ ...prev, [key]: value }));
  };

  return (
    <div className="space-y-6">
      {/* 顶栏 */}
      <div className="flex items-center justify-between">
        <button
          onClick={onBack}
          className="text-accent hover:text-accent-strong text-sm transition"
        >
          {t('admin.monitors.backToList')}
        </button>
        <div className="text-xs text-muted">
          {monitorKey} | rev:{monitorFile.metadata.revision} | {monitorFile.metadata.source}
        </div>
      </div>

      {/* 标题 */}
      <h2 className="text-xl font-bold text-primary">
        {root?.provider}/{root?.service}/{root?.channel}
      </h2>

      {/* Tab 导航 */}
      <nav className="flex gap-1 border-b border-default">
        <SubTabButton
          active={activeTab === 'detail'}
          onClick={() => setActiveTab('detail')}
          label={t('admin.monitors.tabs.detail')}
        />
        <SubTabButton
          active={activeTab === 'logs'}
          onClick={() => setActiveTab('logs')}
          label={t('admin.monitors.tabs.logs')}
        />
      </nav>

      {activeTab === 'logs' ? (
        <MonitorLogsTab
          monitorKey={monitorKey}
          monitorFile={monitorFile}
          fetchLogs={fetchLogs}
        />
      ) : (
      <>
      {/* 保存错误 */}
      {saveError && (
        <div className="p-3 bg-danger/10 border border-danger/20 rounded-lg text-danger text-sm">
          {saveError}
        </div>
      )}

      {/* 父通道详情 */}
      <div className="bg-surface rounded-lg border border-default p-4 space-y-3">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-primary">{t('admin.monitors.parentChannel')}</h3>
          {!isEditing ? (
            <button
              onClick={startEditing}
              className="px-3 py-1 text-xs rounded-lg border border-accent/30 text-accent hover:bg-accent/10 transition"
            >
              {t('admin.monitors.edit')}
            </button>
          ) : (
            <div className="flex gap-2">
              <button
                onClick={handleSave}
                disabled={isSaving}
                className="px-3 py-1 text-xs rounded-lg bg-accent/10 text-accent font-medium hover:bg-accent/20 transition disabled:opacity-50"
              >
                {isSaving ? t('admin.detail.saving') : t('admin.detail.save')}
              </button>
              <button
                onClick={cancelEditing}
                className="px-3 py-1 text-xs rounded-lg border border-default text-secondary hover:text-primary transition"
              >
                {t('admin.detail.cancel')}
              </button>
            </div>
          )}
        </div>

        <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <Field label={t('admin.monitors.field.provider')} value={root?.provider} />
          <EditableField
            label={t('admin.monitors.field.providerName')}
            value={isEditing ? editFields.provider_name : root?.provider_name}
            editing={isEditing}
            onChange={v => updateField('provider_name', v)}
          />
          <Field label={t('admin.monitors.field.service')} value={root?.service} />
          <Field label={t('admin.monitors.field.channel')} value={root?.channel} />
          <EditableField
            label={t('admin.monitors.field.channelName')}
            value={isEditing ? editFields.channel_name : root?.channel_name}
            editing={isEditing}
            onChange={v => updateField('channel_name', v)}
          />
          <EditableSelectField
            label={t('admin.monitors.field.template')}
            value={isEditing ? editFields.template : root?.template}
            editing={isEditing}
            onChange={v => updateField('template', v)}
            options={templateOptions}
          />
          <EditableField
            label={t('admin.monitors.field.baseUrl')}
            value={isEditing ? editFields.base_url : root?.base_url}
            editing={isEditing}
            onChange={v => updateField('base_url', v)}
          />
          <EditableSelectField
            label={t('admin.monitors.field.category')}
            value={isEditing ? editFields.category : root?.category}
            editing={isEditing}
            onChange={v => updateField('category', v)}
            options={categoryOptions}
          />
          <EditableSelectField
            label={t('admin.monitors.field.sponsorLevel')}
            value={isEditing ? editFields.sponsor_level : root?.sponsor_level}
            editing={isEditing}
            onChange={v => updateField('sponsor_level', v)}
            options={sponsorLevelOptions}
          />
          <EditableSelectField
            label={t('admin.monitors.field.board')}
            value={isEditing ? editFields.board : (root?.board || 'hot')}
            editing={isEditing}
            onChange={v => updateField('board', v)}
            options={boardOptions}
          />
          <EditableField
            label={t('admin.monitors.field.interval')}
            value={isEditing ? editFields.interval : root?.interval}
            editing={isEditing}
            onChange={v => updateField('interval', v)}
          />
          <ApiKeyField
            label={t('admin.monitors.field.apiKey')}
            apiKey={isEditing ? (editFields.api_key || '') : (root?.api_key || '')}
            editing={isEditing}
            onChange={v => updateField('api_key', v)}
          />
          <EditableField
            label={t('admin.monitors.field.proxy')}
            value={isEditing ? editFields.proxy : root?.proxy}
            editing={isEditing}
            onChange={v => updateField('proxy', v)}
          />
          <EditableField
            label={t('admin.monitors.field.listedSince')}
            value={isEditing ? editFields.listed_since : root?.listed_since}
            editing={isEditing}
            onChange={v => updateField('listed_since', v)}
            type="date"
          />
          <EditableField
            label={t('admin.monitors.field.expiresAt')}
            value={isEditing ? editFields.expires_at : root?.expires_at}
            editing={isEditing}
            onChange={v => updateField('expires_at', v)}
            type="date"
          />
          <EditableField
            label={t('admin.monitors.field.priceMin')}
            value={isEditing ? priceMinRaw : (root?.price_min != null ? String(root.price_min) : '')}
            editing={isEditing}
            onChange={v => {
              if (v !== '' && !/^\d*\.?\d*$/.test(v)) return;
              setPriceMinRaw(v);
              if (priceError) setPriceError('');
            }}
            inputMode="decimal"
            placeholder="如: 0.5"
            error={priceError && priceMinRaw !== '' && !/^\d+\.?\d*$/.test(priceMinRaw.trim()) ? priceError : undefined}
          />
          <EditableField
            label={t('admin.monitors.field.priceMax')}
            value={isEditing ? priceMaxRaw : (root?.price_max != null ? String(root.price_max) : '')}
            editing={isEditing}
            onChange={v => {
              if (v !== '' && !/^\d*\.?\d*$/.test(v)) return;
              setPriceMaxRaw(v);
              if (priceError) setPriceError('');
            }}
            inputMode="decimal"
            placeholder="如: 3.0"
            error={priceError && priceMaxRaw !== '' && !/^\d+\.?\d*$/.test(priceMaxRaw.trim()) ? priceError : undefined}
          />
          <EditableField
            label={t('admin.monitors.field.providerUrl')}
            value={isEditing ? editFields.provider_url : root?.provider_url}
            editing={isEditing}
            onChange={v => updateField('provider_url', v)}
            type="url"
          />
          <EditableSelectField
            label={t('admin.monitors.field.keyType')}
            value={isEditing ? (editFields.key_type || '') : (root?.key_type || '')}
            editing={isEditing}
            onChange={v => updateField('key_type', v)}
            options={[
              { value: '', label: t('admin.monitors.keyTypeDefault') },
              { value: 'official', label: t('admin.monitors.keyTypeOfficial') },
              { value: 'user', label: t('admin.monitors.keyTypeUser') },
            ]}
          />
          <EditableBoolField
            label={t('admin.monitors.field.autoColdExempt')}
            hint={t('admin.monitors.field.autoColdExemptHint')}
            value={isEditing ? (editFields.auto_cold_exempt ?? false) : (root?.auto_cold_exempt ?? false)}
            editing={isEditing}
            onChange={v => updateField('auto_cold_exempt', v)}
          />
          <EditableBoolField
            label={t('admin.monitors.field.autoMoveExempt')}
            hint={t('admin.monitors.field.autoMoveExemptHint')}
            value={isEditing ? (editFields.auto_move_exempt ?? false) : (root?.auto_move_exempt ?? false)}
            editing={isEditing}
            onChange={v => updateField('auto_move_exempt', v)}
          />
        </div>

        {/* 状态切换 */}
        <div className="flex gap-3 pt-2">
          <button
            onClick={() => onToggle('disabled', !root?.disabled)}
            className={`px-3 py-1.5 text-xs rounded-lg border transition ${
              root?.disabled
                ? 'border-success/30 text-success hover:bg-success/10'
                : 'border-danger/30 text-danger hover:bg-danger/10'
            }`}
          >
            {root?.disabled ? t('admin.monitors.enable') : t('admin.monitors.disable')}
          </button>
          <button
            onClick={() => onToggle('hidden', !root?.hidden)}
            className={`px-3 py-1.5 text-xs rounded-lg border transition ${
              root?.hidden
                ? 'border-success/30 text-success hover:bg-success/10'
                : 'border-warning/30 text-warning hover:bg-warning/10'
            }`}
          >
            {root?.hidden ? t('admin.monitors.show') : t('admin.monitors.hide')}
          </button>
        </div>
      </div>

      {/* 子通道 */}
      <div className="bg-surface rounded-lg border border-default p-4 space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-primary">
            {t('admin.monitors.childChannels')} ({isEditing ? editChildren.length : children.length})
          </h3>
          {isEditing && (
            <button
              onClick={addChild}
              className="px-3 py-1 text-xs rounded-lg border border-dashed border-accent/40 text-accent hover:bg-accent/5 transition"
            >
              + {t('admin.monitors.addChild')}
            </button>
          )}
        </div>

        {isEditing ? (
          /* 编辑态：每个子通道展开为可编辑行 */
          editChildren.length === 0 ? (
            <p className="text-xs text-muted">{t('admin.monitors.noChildren')}</p>
          ) : (
            <div className="space-y-3">
              {editChildren.map((child, i) => (
                <div key={i} className="grid grid-cols-[1fr_1fr_1fr_1fr_1fr_auto] gap-2 items-end border-b border-default/30 pb-3 last:border-0">
                  <div>
                    <label className="block text-xs text-muted mb-0.5">{t('admin.monitors.field.model')}</label>
                    <input
                      value={child.model}
                      onChange={e => updateChild(i, 'model', e.target.value)}
                      placeholder={t('admin.monitors.form.modelPlaceholder')}
                      className={fieldInputClass({ dense: true })}
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-muted mb-0.5">{t('admin.monitors.field.modelVendor')}</label>
                    <select
                      value={child.model_vendor}
                      onChange={e => updateChild(i, 'model_vendor', e.target.value)}
                      className={`w-full ${fieldShapeClass({ dense: true })}`}
                    >
                      {vendorOptions(child.model_vendor).map(opt => (
                        <option key={opt.value} value={opt.value}>{opt.label}</option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs text-muted mb-0.5">{t('admin.monitors.field.template')}</label>
                    <select
                      value={child.template}
                      onChange={e => updateChild(i, 'template', e.target.value)}
                      className={`w-full ${fieldShapeClass({ dense: true })}`}
                    >
                      {withCurrentOption(templateOptions, child.template).map(opt => (
                        <option key={opt.value} value={opt.value}>{opt.label}</option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs text-muted mb-0.5">{t('admin.monitors.field.baseUrl')}</label>
                    <input
                      value={child.base_url}
                      onChange={e => updateChild(i, 'base_url', e.target.value)}
                      className={`w-full ${fieldShapeClass({ dense: true })}`}
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-muted mb-0.5">{t('admin.monitors.field.apiKey')}</label>
                    <input
                      type="password"
                      value={child.api_key}
                      onChange={e => updateChild(i, 'api_key', e.target.value)}
                      className={`w-full ${fieldShapeClass({ dense: true })}`}
                    />
                  </div>
                  <button
                    onClick={() => removeChild(i)}
                    className="px-2 py-1 text-danger hover:text-danger/80 transition"
                    title={t('admin.monitors.removeChild')}
                    aria-label={t('admin.monitors.removeChild')}
                  >
                    <X className="w-4 h-4" aria-hidden="true" />
                  </button>
                </div>
              ))}
            </div>
          )
        ) : (
          /* 查看态：逐个子通道，可独立测试 */
          childTargets.length === 0 ? (
            children.length === 0 ? (
              <p className="text-xs text-muted">{t('admin.monitors.noChildren')}</p>
            ) : (
              /* 详情接口未返回 probe_targets（旧后端/尚未生效）：退回 raw 列表，仅展示不可测 */
              <div className="space-y-2">
                {children.map((child, i) => (
                  <div key={i} className="flex items-center gap-4 text-sm py-1.5 border-b border-default/30 last:border-0">
                    <span className="text-primary font-medium">{child.model || t('admin.monitors.noModel')}</span>
                    <span className="text-muted">{child.template}</span>
                    {child.base_url && <span className="text-muted text-xs truncate max-w-[200px]">{child.base_url}</span>}
                  </div>
                ))}
              </div>
            )
          ) : (
            <div className="space-y-2">
              {childTargets.map((target, i) => {
                // 仅在有 resolved model 时才读结果桶——空 model（runtime 未生效的 raw 回退）
                // 不可探测，且绝不能落到父通道的 '' 桶（否则会把父结果错显到子行）。
                const canProbe = !!target.model && !templateDirty;
                const rowProbing = !!(target.model && probingTargets[target.model]);
                const rowResult = target.model ? probeResults[target.model] : undefined;
                const rowError = target.model ? probeErrors[target.model] : undefined;
                return (
                  <div key={`${target.model}-${target.template}-${i}`} className="py-1.5 border-b border-default/30 last:border-0 space-y-1.5">
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-sm">
                      <span className="text-primary font-medium">{target.model || t('admin.monitors.noModel')}</span>
                      <span className="text-muted">{target.template}</span>
                      <button
                        type="button"
                        onClick={() => handleChildProbe(target.model)}
                        disabled={!canProbe || rowProbing}
                        title={!target.model ? t('admin.monitors.probeTargetPending') : undefined}
                        className="px-2.5 py-1 rounded-lg bg-accent/10 text-accent text-xs font-medium hover:bg-accent/20 transition disabled:opacity-50"
                      >
                        {rowProbing ? t('admin.monitors.probing') : t('admin.monitors.probe')}
                      </button>
                      <ProbeResultLine result={rowResult} error={rowError} />
                    </div>
                    {/* 子通道 api_key 由后端继承解析、前端不持有，故 curl 仅提供脱敏版 */}
                    <ProbeResultDetail result={rowResult} apiKey="" />
                  </div>
                );
              })}
            </div>
          )
        )}
      </div>

      {/* 操作栏 */}
      <div className="flex gap-3">
        <button
          onClick={handleProbe}
          disabled={parentProbing || templateDirty}
          title={templateDirty ? t('admin.monitors.templateDirtyHint') : undefined}
          className="px-4 py-2 rounded-lg bg-accent/10 text-accent text-sm font-medium hover:bg-accent/20 transition disabled:opacity-50"
        >
          {parentProbing ? t('admin.monitors.probing') : t('admin.monitors.probe')}
        </button>

        {templateDirty && (
          <span className="self-center text-xs text-warning">
            {t('admin.monitors.templateDirtyHint')}
          </span>
        )}

        {!templateDirty && <ProbeResultLine result={parentResult} error={parentError} />}

        <div className="flex-1" />

        {!showDeleteConfirm ? (
          <button
            onClick={() => setShowDeleteConfirm(true)}
            className="px-4 py-2 rounded-lg border border-danger/30 text-danger text-sm hover:bg-danger/10 transition"
          >
            {t('admin.monitors.archive')}
          </button>
        ) : (
          <div className="flex gap-2 items-center">
            <span className="text-xs text-danger">{t('admin.monitors.confirmArchive')}</span>
            <button
              onClick={handleDelete}
              disabled={isDeleting}
              className="px-3 py-1.5 rounded-lg bg-danger text-white text-xs font-medium hover:bg-danger/80 transition disabled:opacity-50"
            >
              {isDeleting ? '...' : t('admin.monitors.confirmYes')}
            </button>
            <button
              onClick={() => setShowDeleteConfirm(false)}
              className="px-3 py-1.5 rounded-lg border border-default text-secondary text-xs hover:text-primary transition"
            >
              {t('admin.detail.cancel')}
            </button>
          </div>
        )}
      </div>

      {!templateDirty && <ProbeResultDetail result={parentResult} apiKey={editFields.api_key} />}
      </>
      )}
    </div>
  );
}

/** 探测结果摘要行：状态点 + 延迟 + HTTP + 细分状态标签 + 完整错误信息。
 *  细分状态复用主表 subStatus 命名空间（缺失键回退原始字符串）；成功（probeStatus===1
 *  或 sub_status==='none'）时不显示细分状态。错误信息完整换行展示，不再截断。 */
function ProbeResultLine({ result, error }: { result?: ProbeResult | null; error?: string | null }) {
  const { t } = useTranslation();
  if (!result && !error) return null;

  const showSubStatus = !!result?.subStatus && result.subStatus !== 'none' && result.probeStatus !== 1;

  return (
    <div className="flex flex-wrap items-center gap-3 text-xs min-w-0">
      {result && (
        <>
          <span className={`inline-block w-2 h-2 rounded-full shrink-0 ${
            result.probeStatus === 1 ? 'bg-success' :
            result.probeStatus === 2 ? 'bg-warning' : 'bg-danger'
          }`} />
          <span className="text-primary">{result.latency}ms</span>
          <span className="text-muted">HTTP {result.httpCode}</span>
          {result.viaProxy && (
            <span className="rounded bg-secondary/10 px-1.5 py-0.5 text-secondary">{t('admin.monitors.viaProxy')}</span>
          )}
          {showSubStatus && (
            <span className="text-secondary">{t(`subStatus.${result.subStatus}`, result.subStatus)}</span>
          )}
          {result.errorMessage && (
            <span className="text-danger whitespace-pre-wrap break-all">{result.errorMessage}</span>
          )}
        </>
      )}
      {error && <span className="text-danger whitespace-pre-wrap break-all">{error}</span>}
    </div>
  );
}

/** 探测结果的详细诊断：上游响应体片段 + 可复制脱敏 curl。父子通道共用。 */
function ProbeResultDetail({ result, apiKey }: { result?: ProbeResult | null; apiKey?: string }) {
  const { t } = useTranslation();
  if (!result) return null;

  return (
    <>
      {result.responseSnippet && (
        <div className="space-y-1">
          <div className="text-xs text-muted">{t('admin.monitors.probeResponseBody')}</div>
          <pre className="whitespace-pre-wrap break-all text-xs max-h-40 overflow-y-auto bg-surface p-2 rounded text-secondary">
            {result.responseSnippet}
          </pre>
        </div>
      )}
      {result.curl && <CurlCommandBlock curl={result.curl} apiKey={apiKey} />}
    </>
  );
}

function SubTabButton({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return (
    <button
      onClick={onClick}
      className={`px-4 py-2 text-sm font-medium transition border-b-2 -mb-px ${
        active
          ? 'border-accent text-accent'
          : 'border-transparent text-muted hover:text-secondary'
      }`}
    >
      {label}
    </button>
  );
}

function Field({ label, value }: { label: string; value?: string | number | null }) {
  return (
    <div>
      <span className="text-muted">{label}: </span>
      <span className="text-primary">{value != null && value !== '' ? value : '-'}</span>
    </div>
  );
}

function EditableSelectField({
  label, value, editing, onChange, options,
}: {
  label: string;
  value?: string | null;
  editing: boolean;
  onChange: (v: string) => void;
  options: SelectOption[];
}) {
  const currentValue = String(value || '');
  const displayLabel = options.find(o => o.value === currentValue)?.label ?? currentValue;

  if (!editing) {
    return <Field label={label} value={displayLabel || '-'} />;
  }

  return (
    <div>
      <label className="block text-xs text-muted mb-0.5">{label}</label>
      <select
        value={currentValue}
        onChange={e => onChange(e.target.value)}
        className={`w-full ${fieldShapeClass({ dense: true })}`}
      >
        {options.map(opt => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
    </div>
  );
}

function EditableField({
  label, value, editing, onChange, type = 'text', inputMode, placeholder, error,
}: {
  label: string;
  value?: string | number | null;
  editing: boolean;
  onChange: (v: string) => void;
  type?: string;
  inputMode?: InputHTMLAttributes<HTMLInputElement>['inputMode'];
  placeholder?: string;
  error?: string;
}) {
  if (!editing) {
    return <Field label={label} value={value} />;
  }
  return (
    <div>
      <label className="block text-xs text-muted mb-0.5">{label}</label>
      <input
        type={type}
        inputMode={inputMode}
        value={value != null ? String(value) : ''}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        className={fieldInputClass({ dense: true, error: !!error })}
      />
      {error && <p className="mt-0.5 text-xs text-danger">{error}</p>}
    </div>
  );
}

function EditableBoolField({
  label, hint, value, editing, onChange,
}: {
  label: string;
  hint?: string;
  value: boolean;
  editing: boolean;
  onChange: (v: boolean) => void;
}) {
  if (!editing) {
    return (
      <div>
        <span className="text-muted">{label}: </span>
        <span className="text-primary">{value ? '✓' : '-'}</span>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-0.5">
      <label className="flex items-center gap-2 cursor-pointer">
        <input
          type="checkbox"
          checked={value}
          onChange={e => onChange(e.target.checked)}
          className="accent-accent"
        />
        <span className="text-xs text-primary">{label}</span>
      </label>
      {hint && <span className="text-xs text-muted ml-5">{hint}</span>}
    </div>
  );
}

function withCurrentOption(options: SelectOption[], current?: string | null): SelectOption[] {
  if (!current || options.some(o => o.value === current)) return options;
  return [...options, { value: current, label: current }];
}

function ApiKeyField({
  label, apiKey, editing, onChange,
}: {
  label: string;
  apiKey: string;
  editing: boolean;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    if (!apiKey) return;
    try {
      await navigator.clipboard.writeText(apiKey);
    } catch {
      const input = document.createElement('input');
      input.value = apiKey;
      document.body.appendChild(input);
      input.select();
      document.execCommand('copy');
      document.body.removeChild(input);
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const revealTitle = revealed ? t('admin.monitors.hideKey') : t('admin.monitors.showKey');
  const copyTitle = copied ? t('admin.monitors.copiedKey') : t('admin.monitors.copyKey');

  if (!editing) {
    const displayValue = apiKey ? (revealed ? apiKey : `***${apiKey.slice(-4)}`) : '';
    return (
      <div>
        <span className="text-muted">{label}: </span>
        <span className="text-primary break-all">{displayValue || '-'}</span>
        {apiKey && (
          <span className="inline-flex gap-1 ml-2 align-middle">
            <button
              type="button"
              onClick={() => setRevealed(v => !v)}
              className="text-muted hover:text-accent transition"
              title={revealTitle}
              aria-label={revealTitle}
            >
              {revealed ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
            </button>
            <button
              type="button"
              onClick={handleCopy}
              className="text-muted hover:text-accent transition"
              title={copyTitle}
              aria-label={copyTitle}
            >
              {copied ? <Check className="w-3.5 h-3.5 text-success" /> : <Copy className="w-3.5 h-3.5" />}
            </button>
          </span>
        )}
      </div>
    );
  }

  return (
    <div>
      <label className="block text-xs text-muted mb-0.5">{label}</label>
      <div className="flex items-center gap-1">
        <input
          type={revealed ? 'text' : 'password'}
          value={apiKey}
          onChange={e => onChange(e.target.value)}
          className={`flex-1 min-w-0 ${fieldShapeClass({ dense: true })}`}
        />
        <button
          type="button"
          onClick={() => setRevealed(v => !v)}
          className="p-1 text-muted hover:text-accent transition"
          title={revealTitle}
          aria-label={revealTitle}
        >
          {revealed ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
        </button>
        <button
          type="button"
          onClick={handleCopy}
          disabled={!apiKey}
          className="p-1 text-muted hover:text-accent transition disabled:opacity-30"
          title={copyTitle}
          aria-label={copyTitle}
        >
          {copied ? <Check className="w-4 h-4 text-success" /> : <Copy className="w-4 h-4" />}
        </button>
      </div>
    </div>
  );
}

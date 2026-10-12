/** 管理后台共享表单控件 */
import type React from 'react';
import { fieldInputClass, fieldSelectClass } from './fieldStyles';

/** 文本 / 数字 / URL 输入框（支持多行 textarea） */
export function FormField({
  label,
  value,
  onChange,
  type = 'text',
  placeholder,
  multiline = false,
  inputMode,
  error,
  maxLength,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  placeholder?: string;
  multiline?: boolean;
  inputMode?: React.InputHTMLAttributes<HTMLInputElement>['inputMode'];
  error?: string;
  maxLength?: number;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-muted mb-1">{label}</label>
      {multiline ? (
        <textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onBlur={() => { const trimmed = value.trim(); if (trimmed !== value) onChange(trimmed); }}
          placeholder={placeholder}
          maxLength={maxLength}
          rows={3}
          className={`${fieldInputClass()} resize-y`}
        />
      ) : (
        <input
          type={type}
          inputMode={inputMode}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onBlur={() => { const trimmed = value.trim(); if (trimmed !== value) onChange(trimmed); }}
          placeholder={placeholder}
          maxLength={maxLength}
          className={fieldInputClass({ error: !!error })}
        />
      )}
      {error && <p className="mt-1 text-xs text-danger">{error}</p>}
    </div>
  );
}

/** 下拉选择框 */
export function SelectField({
  label,
  value,
  onChange,
  options,
  disabled = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  disabled?: boolean;
}) {
  return (
    <div>
      <label className="block text-xs font-medium text-muted mb-1">{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className={`${fieldSelectClass()} ${disabled ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer'}`}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
    </div>
  );
}

/** 只读字段展示 */
export function ReadOnlyField({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <label className="block text-xs font-medium text-muted mb-1">{label}</label>
      <div className="px-3 py-2 bg-elevated/50 border border-default rounded-md text-secondary text-sm">
        {value || '--'}
      </div>
    </div>
  );
}

/** 复选框（含可选提示文字） */
export function CheckboxField({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <label className="flex items-center gap-2 cursor-pointer">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          className="accent-accent"
        />
        <span className="text-sm text-primary">{label}</span>
      </label>
      {hint && <span className="text-xs text-muted ml-5">{hint}</span>}
    </div>
  );
}

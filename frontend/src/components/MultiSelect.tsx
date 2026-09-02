import { useState, useMemo, useRef, useEffect, useCallback } from 'react';
import { Check, ChevronDown, Minus, Search, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export interface MultiSelectOption {
  value: string;
  label: string;
  /**
   * 可选分组键。带该字段的选项会聚合到同一分组下，**分组顺序由选项在数组中的
   * 首次出现顺序决定**——调用方按业务顺序排好数组即可，组件不重排。
   *
   * 不传 = 扁平模式，渲染与交互与分组特性引入前逐字一致（provider/service/
   * channel/vendor 四个既有筛选器都走这条路径）。
   */
  groupKey?: string;
  /** 分组标题展示名。缺省时回退用 groupKey 本身，避免标题空着。 */
  groupLabel?: string;
}

/** 分组的选中态。用于组标题的三态图标与 aria 语义。 */
type GroupSelectionState = 'all' | 'partial' | 'none';

interface OptionGroup {
  key: string;
  label: string;
  options: MultiSelectOption[];
}

interface MultiSelectProps {
  value: string[];
  options: MultiSelectOption[];
  onChange: (values: string[]) => void;
  placeholder?: string;
  searchable?: boolean;
  disabled?: boolean;
  className?: string;
}

/**
 * 多选下拉组件
 * - 空数组表示"全部"
 * - 支持搜索过滤
 * - 支持键盘操作
 * - 点击外部自动关闭
 */
export function MultiSelect({
  value,
  options,
  onChange,
  placeholder,
  searchable = true,
  disabled = false,
  className = '',
}: MultiSelectProps) {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  // 默认占位符
  const displayPlaceholder = placeholder ?? t('controls.filters.provider');

  // 搜索过滤
  const filteredOptions = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return options;
    return options.filter(opt =>
      opt.label.toLowerCase().includes(term) ||
      opt.value.toLowerCase().includes(term)
    );
  }, [options, search]);

  // 点击外部关闭
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
        setSearch('');
      }
    };

    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [isOpen]);

  // 打开时聚焦搜索框
  useEffect(() => {
    if (isOpen && searchable && searchInputRef.current) {
      searchInputRef.current.focus();
    }
  }, [isOpen, searchable]);

  // 分组聚合。只聚合**搜索后仍可见**的叶子，故组内全被搜索过滤掉时该组标题
  // 也不会渲染——不留一个点开是空的孤儿标题。
  const groups = useMemo<OptionGroup[]>(() => {
    const byKey = new Map<string, OptionGroup>();
    const ordered: OptionGroup[] = [];

    for (const option of filteredOptions) {
      const key = option.groupKey?.trim();
      if (!key) continue;

      let group = byKey.get(key);
      if (!group) {
        group = { key, label: option.groupLabel?.trim() || key, options: [] };
        byKey.set(key, group);
        ordered.push(group);
      }
      group.options.push(option);
    }
    return ordered;
  }, [filteredOptions]);

  /** 没有任何选项声明 groupKey 时走扁平模式（既有四个筛选器均如此）。 */
  const isGrouped = groups.length > 0;

  /** 分组模式下仍可能有未归组的散项，它们排在所有分组之后。 */
  const ungroupedOptions = useMemo(
    () => (isGrouped ? filteredOptions.filter(option => !option.groupKey?.trim()) : filteredOptions),
    [filteredOptions, isGrouped],
  );

  // 切换选中状态
  const toggleOption = useCallback((optionValue: string) => {
    const isSelected = value.includes(optionValue);
    if (isSelected) {
      onChange(value.filter(v => v !== optionValue));
    } else {
      onChange([...value, optionValue]);
    }
  }, [value, onChange]);

  /**
   * 组选中态。**只统计当前可见（搜索后）的叶子**——用户看到什么，"全选"就作用于
   * 什么。若改成统计被搜索藏起来的叶子，会出现「组内可见项全勾上了、标题却显示
   * 半选」的费解状态。
   *
   * 一并返回计数是给 a11y 用的：aria-selected 只有 true/false、表达不了半选，
   * 屏幕阅读器要靠标题上的 "1/2" 才能区分「部分选中」与「未选」。
   */
  const getGroupState = useCallback((groupOptions: MultiSelectOption[]): {
    state: GroupSelectionState;
    selected: number;
    total: number;
  } => {
    const selected = groupOptions.filter(option => value.includes(option.value)).length;
    const total = groupOptions.length;
    const state: GroupSelectionState = selected === 0 ? 'none' : selected === total ? 'all' : 'partial';
    return { state, selected, total };
  }, [value]);

  /**
   * 点组标题：整组已全选则整组取消，否则补齐该组缺的叶子。
   *
   * 刻意**不做**「选满全部叶子就归一成空数组（=全部）」那种聪明化简：空数组是
   * 「未筛选」，显式选中每一项是「已筛选」，两者在 URL 分享、activeFiltersCount
   * 和联动选项计算上表现都不同，静默改写会让用户的显式操作变成另一件事。
   */
  const toggleGroup = useCallback((groupOptions: MultiSelectOption[]) => {
    const groupValues = groupOptions.map(option => option.value);
    if (groupValues.length === 0) return;

    const groupValueSet = new Set(groupValues);
    if (groupValues.every(optionValue => value.includes(optionValue))) {
      onChange(value.filter(optionValue => !groupValueSet.has(optionValue)));
      return;
    }

    const selected = new Set(value);
    onChange([...value, ...groupValues.filter(optionValue => !selected.has(optionValue))]);
  }, [value, onChange]);

  // 全选/全不选
  const handleSelectAll = useCallback(() => {
    onChange([]);  // 空数组 = 全部
    setIsOpen(false);
    setSearch('');
  }, [onChange]);

  // 清空选择
  const handleClear = useCallback((e: React.MouseEvent | React.KeyboardEvent) => {
    e.stopPropagation();
    onChange([]);
  }, [onChange]);

  // 键盘操作
  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      setIsOpen(false);
      setSearch('');
    } else if (e.key === 'Enter' && !isOpen) {
      setIsOpen(true);
    }
  }, [isOpen]);

  // 显示文本
  const displayText = useMemo(() => {
    if (value.length === 0) {
      return displayPlaceholder;
    }
    if (value.length === 1) {
      const opt = options.find(o => o.value === value[0]);
      return opt?.label ?? value[0];
    }
    return t('controls.multiSelect.selectedCount', { count: value.length });
  }, [value, options, displayPlaceholder, t]);

  const isAllSelected = value.length === 0;
  const hasVisibleOptions = filteredOptions.length > 0;

  /** 叶子选项。分组模式下多一级缩进，让层级一眼可辨。 */
  const renderOption = (option: MultiSelectOption, indented: boolean) => {
    const isSelected = value.includes(option.value);
    return (
      <button
        key={option.value}
        type="button"
        role="option"
        aria-selected={isSelected}
        onClick={() => toggleOption(option.value)}
        className={`
          w-full flex items-center gap-2 ${indented ? 'pl-7 pr-3' : 'px-3'} py-2 text-sm text-left
          transition-colors
          ${isSelected
            ? 'bg-accent/10 text-accent'
            : 'text-secondary hover:bg-muted/50'
          }
        `}
      >
        <span className={`
          w-4 h-4 rounded border flex items-center justify-center flex-shrink-0
          ${isSelected
            ? 'bg-accent border-accent'
            : 'border-strong'
          }
        `}>
          {isSelected && <Check size={12} className="text-inverse" />}
        </span>
        <span className="truncate">{option.label}</span>
      </button>
    );
  };

  return (
    <div
      ref={containerRef}
      className={`relative overflow-visible min-w-0 ${className}`}
      onKeyDown={handleKeyDown}
    >
      {/* 触发按钮 */}
      <div
        role="button"
        tabIndex={disabled ? -1 : 0}
        onClick={() => !disabled && setIsOpen(!isOpen)}
        onKeyDown={(e) => {
          if (!disabled && (e.key === 'Enter' || e.key === ' ')) {
            e.preventDefault();
            setIsOpen(!isOpen);
          }
        }}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
        aria-disabled={disabled}
        className={`
          flex items-center justify-between gap-1.5 w-full lg:w-auto min-w-0
          bg-elevated text-primary text-sm rounded-lg
          border border-default px-2 h-8 outline-none
          transition-all hover:bg-muted
          focus:ring-2 focus:ring-accent focus:border-transparent
          ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}
          ${!isAllSelected ? 'border-accent/50' : ''}
        `}
      >
        <span className={`truncate min-w-0 ${isAllSelected ? 'text-secondary' : 'text-primary'}`}>
          {displayText}
        </span>
        <div className="flex items-center gap-1 flex-shrink-0">
          {!isAllSelected && (
            <button
              type="button"
              onClick={handleClear}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  handleClear(e);
                }
              }}
              className="p-0.5 hover:bg-muted rounded transition-colors focus:outline-none focus:ring-1 focus:ring-accent"
              aria-label={t('common.clear')}
              tabIndex={0}
            >
              <X size={14} className="text-secondary hover:text-primary" />
            </button>
          )}
          <ChevronDown
            size={16}
            className={`text-secondary transition-transform ${isOpen ? 'rotate-180' : ''}`}
          />
        </div>
      </div>

      {/* 下拉面板。
          listbox 语义挂在**真正的选项容器**上（见下方），不挂这层面板：面板里还有
          搜索框，把 textbox 塞进 listbox 子树是非法结构，屏幕阅读器会把它读成一个选项。 */}
      {isOpen && (
        <div
          className="
            absolute z-50 mt-1 min-w-full w-max
            bg-elevated border border-default rounded-lg shadow-xl
            max-h-[320px] flex flex-col
          "
        >
          {/* 搜索框 */}
          {searchable && (
            <div className="p-2 border-b border-default flex-shrink-0">
              <div className="relative">
                <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
                <input
                  ref={searchInputRef}
                  type="text"
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                  placeholder={t('controls.multiSelect.searchPlaceholder')}
                  className="
                    w-full pl-8 pr-3 py-1.5 text-sm
                    bg-surface text-primary rounded-md
                    border border-default outline-none
                    focus:ring-1 focus:ring-accent focus:border-accent
                    placeholder:text-muted
                  "
                />
              </div>
            </div>
          )}

          {/* 选项列表 */}
          <div className="overflow-y-auto flex-1 py-1">
            <div role="listbox" aria-multiselectable="true">
              {/* "全部"选项 - 仅在没有搜索时显示 */}
              {!search.trim() && (
                <button
                  type="button"
                  role="option"
                  aria-selected={isAllSelected}
                  onClick={handleSelectAll}
                  className={`
                    w-full flex items-center gap-2 px-3 py-2 text-sm text-left
                    transition-colors
                    ${isAllSelected
                      ? 'bg-accent/10 text-accent'
                      : 'text-secondary hover:bg-muted/50'
                    }
                  `}
                >
                  <span className={`
                    w-4 h-4 rounded border flex items-center justify-center flex-shrink-0
                    ${isAllSelected
                      ? 'bg-accent border-accent'
                      : 'border-strong'
                    }
                  `}>
                    {isAllSelected && <Check size={12} className="text-inverse" />}
                  </span>
                  <span className="truncate">{displayPlaceholder}</span>
                </button>
              )}

              {/* 分隔线 - 仅在没有搜索时显示 */}
              {!search.trim() && <div role="presentation" className="h-px bg-muted mx-2 my-1" />}

              {/* 分组（可选）。组标题本身是一个 option——点它 = 全选/取消该组，
                  这是「一次勾中所有 Opus」的入口。listbox > group > option 是合法
                  的 ARIA 结构，故标题不能用 checkbox role。 */}
              {groups.map(group => {
                const { state, selected, total } = getGroupState(group.options);
                const isActive = state !== 'none';
                return (
                  <div key={`group-${group.key}`} role="group" aria-label={group.label}>
                    <button
                      type="button"
                      role="option"
                      aria-selected={state === 'all'}
                      // aria-selected 没有 mixed 值，半选与未选都会读成 "not selected"。
                      // 计数放进无障碍名里，屏幕阅读器才能把「一个都没选」和「选了一半」
                      // 区分开——视觉上那个 Minus 图标对它是不存在的。
                      aria-label={`${group.label} (${selected}/${total})`}
                      onClick={() => toggleGroup(group.options)}
                      className={`
                        w-full flex items-center gap-2 px-3 py-2 text-sm text-left font-medium
                        transition-colors
                        ${isActive
                          ? 'bg-accent/10 text-accent'
                          : 'text-secondary hover:bg-muted/50'
                        }
                      `}
                    >
                      <span className={`
                        w-4 h-4 rounded border flex items-center justify-center flex-shrink-0
                        ${isActive
                          ? 'bg-accent border-accent'
                          : 'border-strong'
                        }
                      `}>
                        {state === 'all' && <Check size={12} className="text-inverse" />}
                        {state === 'partial' && <Minus size={12} className="text-inverse" />}
                      </span>
                      <span className="truncate">{group.label}</span>
                    </button>
                    {group.options.map(option => renderOption(option, true))}
                  </div>
                );
              })}

              {/* 未归组的散项；扁平模式下这就是全部选项。 */}
              {ungroupedOptions.map(option => renderOption(option, false))}
            </div>

            {!hasVisibleOptions && (
              <div role="status" className="px-3 py-4 text-sm text-muted text-center">
                {t('controls.multiSelect.noResults')}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

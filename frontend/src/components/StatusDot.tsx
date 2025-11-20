import { STATUS } from '../constants';
import type { StatusKey } from '../types';

interface StatusDotProps {
  status: StatusKey;
  size?: 'sm' | 'md';
}

export function StatusDot({ status, size = 'md' }: StatusDotProps) {
  const sizeClass = size === 'sm' ? 'w-2 h-2' : 'w-3 h-3';

  return (
    <div
      className={`${sizeClass} rounded-full ${STATUS[status].color} ${STATUS[status].glow} transition-all duration-500`}
    />
  );
}

import { useCallback, useState } from 'react';

// 访客端画作列开关的 localStorage key
const DRAWING_COLUMN_KEY = 'relay-pulse-drawing-column';

export interface DrawingColumnPreference {
  showDrawings: boolean;
  toggleDrawings: () => void;
}

/** 画作列的访客开关（持久化到 localStorage，默认开）。站点级的 hide_drawing_column 另算。 */
export function useDrawingColumnPreference(): DrawingColumnPreference {
  const [showDrawings, setShowDrawings] = useState(() => {
    try {
      return localStorage.getItem(DRAWING_COLUMN_KEY) !== 'false';
    } catch {
      return true;
    }
  });

  const toggleDrawings = useCallback(() => {
    setShowDrawings((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(DRAWING_COLUMN_KEY, String(next));
      } catch {
        // ignore
      }
      return next;
    });
  }, []);

  return { showDrawings, toggleDrawings };
}

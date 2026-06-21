/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import { useEffect, useRef, useState } from 'react';
import { initVChartSemiTheme } from '@visactor/vchart-semi-theme';

export const useDashboardChartTheme = (actualTheme) => {
  const themeVersionRef = useRef(0);
  const [chartThemeKey, setChartThemeKey] = useState(
    () => `${actualTheme || 'light'}-0`,
  );

  useEffect(() => {
    let rafId = null;
    const nextTheme = actualTheme || 'light';

    const updateChartThemeKey = () => {
      themeVersionRef.current += 1;
      setChartThemeKey(`${nextTheme}-${themeVersionRef.current}`);
    };

    if (typeof window === 'undefined') {
      updateChartThemeKey();
      return undefined;
    }

    initVChartSemiTheme({
      isWatchingThemeSwitch: true,
    });

    if (!window.requestAnimationFrame) {
      updateChartThemeKey();
      return undefined;
    }

    rafId = window.requestAnimationFrame(updateChartThemeKey);

    return () => {
      if (rafId) {
        window.cancelAnimationFrame(rafId);
      }
    };
  }, [actualTheme]);

  return chartThemeKey;
};

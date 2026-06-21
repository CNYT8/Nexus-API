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
import {
  generateVChartSemiTheme,
  initVChartSemiTheme,
  switchVChartSemiTheme,
} from '@visactor/vchart-semi-theme';

let dashboardVChartSemiThemeInitialized = false;

const getSemiThemeMode = (themeMode) =>
  themeMode === 'dark' ? 'dark' : 'light';

const initDashboardVChartSemiTheme = () => {
  if (
    dashboardVChartSemiThemeInitialized ||
    typeof window === 'undefined'
  ) {
    return;
  }

  initVChartSemiTheme({
    isWatchingThemeSwitch: true,
  });
  dashboardVChartSemiThemeInitialized = true;
};

const refreshDashboardVChartSemiTheme = (themeMode) => {
  if (typeof document === 'undefined' || !document.body) {
    return;
  }

  const mode = getSemiThemeMode(themeMode);

  // Warm mode is implemented as a light Semi theme with overridden CSS tokens.
  // Force-regenerate the VChart theme after those tokens are applied.
  switchVChartSemiTheme(
    true,
    mode,
    generateVChartSemiTheme(mode, document.body),
  );
};

export const useDashboardChartTheme = (actualTheme) => {
  const themeVersionRef = useRef(0);
  const [chartThemeKey, setChartThemeKey] = useState(
    () => `${actualTheme || 'light'}-0`,
  );

  useEffect(() => {
    const frameIds = [];
    const nextTheme = actualTheme || 'light';
    let isCurrent = true;

    const updateChartThemeKey = () => {
      if (!isCurrent) return;

      refreshDashboardVChartSemiTheme(nextTheme);
      themeVersionRef.current += 1;
      setChartThemeKey(`${nextTheme}-${themeVersionRef.current}`);
    };

    if (typeof window === 'undefined') {
      updateChartThemeKey();
      return undefined;
    }

    initDashboardVChartSemiTheme();

    if (!window.requestAnimationFrame) {
      updateChartThemeKey();
      return undefined;
    }

    frameIds.push(
      window.requestAnimationFrame(() => {
        frameIds.push(window.requestAnimationFrame(updateChartThemeKey));
      }),
    );

    return () => {
      isCurrent = false;
      frameIds.forEach((frameId) => window.cancelAnimationFrame(frameId));
    };
  }, [actualTheme]);

  return chartThemeKey;
};

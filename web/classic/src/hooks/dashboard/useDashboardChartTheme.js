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

let dashboardVChartSemiThemeInitialized = false;

const FALLBACK_CHART_THEMES = {
  light: {
    type: 'light',
    backgroundColor: 'transparent',
    textColor: '#1f2329',
    secondaryTextColor: '#646a73',
    axisColor: '#dee0e3',
    gridColor: '#f0f0f0',
    tooltipBackgroundColor: '#ffffff',
    tooltipBorderColor: '#dee0e3',
  },
  dark: {
    type: 'light',
    backgroundColor: 'transparent',
    textColor: '#f5f6f7',
    secondaryTextColor: '#c9cdd4',
    axisColor: 'rgba(255, 255, 255, 0.18)',
    gridColor: 'rgba(255, 255, 255, 0.1)',
    tooltipBackgroundColor: '#1f2329',
    tooltipBorderColor: 'rgba(255, 255, 255, 0.18)',
  },
  warm: {
    type: 'light',
    backgroundColor: 'transparent',
    textColor: '#2f2416',
    secondaryTextColor: '#8a6a43',
    axisColor: 'rgba(120, 53, 15, 0.18)',
    gridColor: 'rgba(120, 53, 15, 0.1)',
    tooltipBackgroundColor: '#fffdf8',
    tooltipBorderColor: 'rgba(120, 53, 15, 0.16)',
  },
};

const getFallbackChartTheme = (themeMode) =>
  FALLBACK_CHART_THEMES[themeMode] || FALLBACK_CHART_THEMES.light;

const isResolvedColor = (color) =>
  color &&
  !color.includes('var(') &&
  !color.includes('undefined') &&
  color !== 'transparent';

const resolveSemiColor = (name, property, fallback) => {
  if (
    typeof window === 'undefined' ||
    !window.getComputedStyle ||
    typeof document === 'undefined' ||
    !document.body
  ) {
    return fallback;
  }

  const probe = document.createElement('span');
  probe.style[property] = `var(${name})`;
  probe.style.position = 'absolute';
  probe.style.pointerEvents = 'none';
  probe.style.visibility = 'hidden';
  document.body.appendChild(probe);

  const color = window.getComputedStyle(probe)[property];
  document.body.removeChild(probe);

  return isResolvedColor(color) ? color : fallback;
};

const readDashboardChartTheme = (themeMode) => {
  const fallback = getFallbackChartTheme(themeMode);

  if (typeof window === 'undefined' || !window.getComputedStyle) {
    return fallback;
  }

  const backgroundColor = 'transparent';
  const textColor = resolveSemiColor(
    '--semi-color-text-0',
    'color',
    fallback.textColor,
  );
  const secondaryTextColor = resolveSemiColor(
    '--semi-color-text-2',
    'color',
    fallback.secondaryTextColor,
  );
  const axisColor = resolveSemiColor(
    '--semi-color-border',
    'borderColor',
    fallback.axisColor,
  );
  const gridColor = resolveSemiColor(
    '--semi-color-border-light',
    'borderColor',
    fallback.gridColor,
  );
  const tooltipBackgroundColor = resolveSemiColor(
    '--semi-color-bg-overlay',
    'backgroundColor',
    resolveSemiColor(
      '--semi-color-bg-0',
      'backgroundColor',
      fallback.tooltipBackgroundColor,
    ),
  );

  return {
    backgroundColor,
    textColor,
    secondaryTextColor,
    axisColor,
    gridColor,
    tooltipBackgroundColor,
    tooltipBorderColor: axisColor,
  };
};

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

export const useDashboardChartTheme = (actualTheme) => {
  const themeVersionRef = useRef(0);
  const [chartThemeState, setChartThemeState] = useState(() => {
    const themeMode = actualTheme || 'light';

    return {
      key: `${themeMode}-0`,
      palette: readDashboardChartTheme(themeMode),
    };
  });

  useEffect(() => {
    let rafId = null;
    const nextTheme = actualTheme || 'light';

    const updateChartTheme = () => {
      themeVersionRef.current += 1;
      setChartThemeState({
        key: `${nextTheme}-${themeVersionRef.current}`,
        palette: readDashboardChartTheme(nextTheme),
      });
    };

    if (typeof window === 'undefined') {
      updateChartTheme();
      return undefined;
    }

    initDashboardVChartSemiTheme();

    if (!window.requestAnimationFrame) {
      updateChartTheme();
      return undefined;
    }

    rafId = window.requestAnimationFrame(updateChartTheme);

    return () => {
      if (rafId) {
        window.cancelAnimationFrame(rafId);
      }
    };
  }, [actualTheme]);

  return chartThemeState;
};

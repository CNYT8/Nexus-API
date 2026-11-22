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

import { useState, useEffect, useContext, useCallback, useMemo, useRef } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { UserContext } from '../../context/User';
import { StatusContext } from '../../context/Status';
import {
  isThemeMode,
  useActualTheme,
  useSetTheme,
  useTheme,
} from '../../context/Theme';
import { getLogo, getSystemName, API, showSuccess, showError } from '../../helpers';
import { normalizeLanguage } from '../../i18n/language';
import { createLanguageChangeQueue } from '../../../../shared/language-change-queue';
import { useIsMobile } from './useIsMobile';
import { useSidebarCollapsed } from './useSidebarCollapsed';
import { useMinimumLoadingTime } from './useMinimumLoadingTime';

export const useHeaderBar = ({ onMobileMenuToggle, drawerOpen }) => {
  const { t, i18n } = useTranslation();
  const [userState, userDispatch] = useContext(UserContext);
  const [statusState] = useContext(StatusContext);
  const isMobile = useIsMobile();
  const [collapsed, toggleCollapsed] = useSidebarCollapsed();
  const [logoLoaded, setLogoLoaded] = useState(false);
  const navigate = useNavigate();
  const [currentLang, setCurrentLang] = useState(normalizeLanguage(i18n.language));
  const location = useLocation();
  const userRef = useRef(userState?.user);
  userRef.current = userState?.user;
  const languageSaveRef = useRef(null);
  if (!languageSaveRef.current || languageSaveRef.current.userId !== userState?.user?.id) {
    languageSaveRef.current = {
      userId: userState?.user?.id,
      change: null,
    };
  }
  const languageMountedRef = useRef(true);
  useEffect(() => {
    languageMountedRef.current = true;
    return () => { languageMountedRef.current = false; };
  }, []);

  const loading = statusState?.status === undefined;
  const isLoading = useMinimumLoadingTime(loading, 200);

  const systemName = getSystemName();
  const logo = getLogo();
  const currentDate = new Date();
  const isNewYear = currentDate.getMonth() === 0 && currentDate.getDate() === 1;

  const isSelfUseMode = statusState?.status?.self_use_mode_enabled || false;
  const docsLink = statusState?.status?.docs_link || '';
  const isDemoSiteMode = statusState?.status?.demo_site_enabled || false;

  // 获取顶栏模块配置
  const headerNavModulesConfig = statusState?.status?.HeaderNavModules;

  // 使用useMemo确保headerNavModules正确响应statusState变化
  const headerNavModules = useMemo(() => {
    if (headerNavModulesConfig) {
      try {
        const modules = JSON.parse(headerNavModulesConfig);

        // 处理向后兼容性：如果pricing是boolean，转换为对象格式
        if (typeof modules.pricing === 'boolean') {
          modules.pricing = {
            enabled: modules.pricing,
            requireAuth: false, // 默认不需要登录鉴权
          };
        }

        return modules;
      } catch (error) {
        console.error('解析顶栏模块配置失败:', error);
        return null;
      }
    }
    return null;
  }, [headerNavModulesConfig]);

  // 获取模型广场权限配置
  const pricingRequireAuth = useMemo(() => {
    if (headerNavModules?.pricing) {
      return typeof headerNavModules.pricing === 'object'
        ? headerNavModules.pricing.requireAuth
        : false; // 默认不需要登录
    }
    return false; // 默认不需要登录
  }, [headerNavModules]);

  const isConsoleRoute = location.pathname.startsWith('/console');

  const theme = useTheme();
  const actualTheme = useActualTheme();
  const setTheme = useSetTheme();

  // Logo loading effect
  useEffect(() => {
    setLogoLoaded(false);
    if (!logo) return;
    const img = new Image();
    img.src = logo;
    img.onload = () => setLogoLoaded(true);
  }, [logo]);

  // Send theme to iframe
  useEffect(() => {
    try {
      const iframe = document.querySelector('iframe');
      const cw = iframe && iframe.contentWindow;
      if (cw) {
        cw.postMessage({ themeMode: actualTheme }, '*');
      }
    } catch (e) {
      // Silently ignore cross-origin or access errors
    }
  }, [actualTheme]);

  // Language change effect
  useEffect(() => {
    const handleLanguageChanged = (lng) => {
      const normalizedLang = normalizeLanguage(lng);
      setCurrentLang(normalizedLang);
      try {
        const iframe = document.querySelector('iframe');
        const cw = iframe && iframe.contentWindow;
        if (cw) {
          cw.postMessage({ lang: normalizedLang }, '*');
        }
      } catch (e) {
        // Silently ignore cross-origin or access errors
      }
    };

    i18n.on('languageChanged', handleLanguageChanged);
    return () => {
      i18n.off('languageChanged', handleLanguageChanged);
    };
  }, [i18n]);

  // Actions
  const logout = useCallback(async () => {
    await API.get('/api/user/logout');
    showSuccess(t('注销成功!'));
    userDispatch({ type: 'logout' });
    localStorage.removeItem('user');
    navigate('/login');
  }, [navigate, t, userDispatch]);

  const handleLanguageChange = useCallback(
    async (lang) => {
      const state = languageSaveRef.current;
      if (!state) return;
      state.change ??= createLanguageChangeQueue({
        getLanguage: () => normalizeLanguage(i18n.language),
        changeLanguage: async (language) => {
          await i18n.changeLanguage(language);
          localStorage.setItem('i18nextLng', language);
        },
        isCurrent: () => languageMountedRef.current && languageSaveRef.current === state && userRef.current?.id === state.userId,
        saveLanguage: async (language) => {
          if (state.userId === undefined) return;
          const res = await API.put('/api/user/self', { language });
          if (!res.data.success) throw new Error(res.data.message || t('语言切换失败，请重试'));
        },
        onConfirmed: (language) => {
          const currentUser = userRef.current;
          if (!currentUser) return;
          let settings = {};
          try {
            const parsed = typeof currentUser.setting === 'string'
              ? JSON.parse(currentUser.setting)
              : currentUser.setting;
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) settings = parsed;
          } catch { /* Keep a malformed settings string from blocking language changes. */ }
          const nextUser = {
            ...currentUser,
            setting: JSON.stringify({ ...settings, language }),
          };
          userRef.current = nextUser;
          userDispatch({ type: 'login', payload: nextUser });
          localStorage.setItem('user', JSON.stringify(nextUser));
        },
        onError: () => showError(t('语言切换失败，请重试')),
      });
      await state.change(lang);
    },
    [i18n, t, userDispatch],
  );

  const handleThemeToggle = useCallback(
    (newTheme) => {
      if (!isThemeMode(newTheme)) {
        return;
      }
      setTheme(newTheme);
    },
    [setTheme],
  );

  const handleMobileMenuToggle = useCallback(() => {
    if (isMobile) {
      onMobileMenuToggle();
    } else {
      toggleCollapsed();
    }
  }, [isMobile, onMobileMenuToggle, toggleCollapsed]);

  return {
    // State
    userState,
    statusState,
    isMobile,
    collapsed,
    logoLoaded,
    currentLang,
    location,
    isLoading,
    systemName,
    logo,
    isNewYear,
    isSelfUseMode,
    docsLink,
    isDemoSiteMode,
    isConsoleRoute,
    theme,
    actualTheme,
    drawerOpen,
    headerNavModules,
    pricingRequireAuth,

    // Actions
    logout,
    handleLanguageChange,
    handleThemeToggle,
    handleMobileMenuToggle,
    navigate,
    t,
  };
};

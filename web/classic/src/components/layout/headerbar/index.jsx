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

import React from 'react';
import { useHeaderBar } from '../../../hooks/common/useHeaderBar';
import { useNotifications } from '../../../hooks/common/useNotifications';
import { useNavigation } from '../../../hooks/common/useNavigation';
import NoticeModal from '../NoticeModal';
import MobileMenuButton from './MobileMenuButton';
import HeaderLogo from './HeaderLogo';
import Navigation from './Navigation';
import ActionButtons from './ActionButtons';
import { useLogoAccent } from '../../../hooks/common/useLogoAccent';

const HeaderBar = ({ onMobileMenuToggle, drawerOpen }) => {
  const {
    userState,
    statusState,
    isMobile,
    collapsed,
    logoLoaded,
    currentLang,
    isLoading,
    systemName,
    logo,
    isNewYear,
    isSelfUseMode,
    docsLink,
    isDemoSiteMode,
    isConsoleRoute,
    theme,
    headerNavModules,
    pricingRequireAuth,
    logout,
    handleLanguageChange,
    handleThemeToggle,
    handleMobileMenuToggle,
    navigate,
    t,
  } = useHeaderBar({ onMobileMenuToggle, drawerOpen });

  const {
    noticeVisible,
    unreadCount,
    handleNoticeOpen,
    handleNoticeClose,
    getUnreadKeys,
  } = useNotifications(statusState);

  const { mainNavLinks } = useNavigation(t, docsLink, headerNavModules);
  const logoAccent = useLogoAccent(logo);

  return (
    <header
      className='classic-header text-semi-color-text-0 sticky top-0 z-50 isolate overflow-hidden border-b border-transparent bg-white/60 shadow-[inset_0_1px_0_rgba(255,255,255,0.58),inset_0_-1px_0_rgba(255,255,255,0.16),0_18px_42px_-32px_rgba(15,23,42,0.78)] backdrop-blur-2xl backdrop-brightness-105 backdrop-contrast-125 backdrop-saturate-200 transition-colors duration-300 before:pointer-events-none before:absolute before:inset-x-0 before:top-0 before:h-px before:bg-white/60 after:pointer-events-none after:absolute after:inset-x-3 after:bottom-0 after:h-px after:bg-gradient-to-r after:from-transparent after:via-white/30 after:to-transparent dark:bg-zinc-950/50 dark:shadow-[inset_0_1px_0_rgba(255,255,255,0.12),inset_0_-1px_0_rgba(255,255,255,0.06),0_18px_42px_-32px_rgba(0,0,0,0.98)] dark:before:bg-white/15 dark:after:via-white/15'
      style={
        logoAccent.active
          ? { '--classic-header-logo-accent': logoAccent.rgb }
          : undefined
      }
    >
      <NoticeModal
        visible={noticeVisible}
        onClose={handleNoticeClose}
        isMobile={isMobile}
        defaultTab={unreadCount > 0 ? 'system' : 'inApp'}
        unreadKeys={getUnreadKeys()}
      />

      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 z-0 opacity-95'
        style={{
          background:
            'linear-gradient(115deg, rgba(255,255,255,0.34) 0%, rgba(255,255,255,0.12) 28%, rgba(255,255,255,0.03) 52%, rgba(255,255,255,0.18) 100%), radial-gradient(30rem 10rem at 12% -5rem, rgba(255,255,255,0.34), transparent 72%), radial-gradient(18rem 7rem at 88% -3rem, rgba(255,255,255,0.16), transparent 70%)',
        }}
      />

      {logoAccent.active && (
        <div
          aria-hidden='true'
          className='pointer-events-none absolute inset-0 z-0 opacity-90'
          style={{
            background:
              'radial-gradient(26rem 9rem at 2.5rem -2rem, rgba(var(--classic-header-logo-accent), 0.28), transparent 72%), radial-gradient(20rem 7rem at 42% -2.5rem, rgba(var(--classic-header-logo-accent), 0.12), transparent 74%)',
          }}
        />
      )}

      <div className='relative z-10 w-full px-2'>
        <div className='flex items-center justify-between h-16'>
          <div className='flex items-center'>
            <MobileMenuButton
              isConsoleRoute={isConsoleRoute}
              isMobile={isMobile}
              drawerOpen={drawerOpen}
              collapsed={collapsed}
              onToggle={handleMobileMenuToggle}
              t={t}
            />

            <HeaderLogo
              isMobile={isMobile}
              isConsoleRoute={isConsoleRoute}
              logo={logo}
              logoLoaded={logoLoaded}
              isLoading={isLoading}
              systemName={systemName}
              isSelfUseMode={isSelfUseMode}
              isDemoSiteMode={isDemoSiteMode}
              t={t}
            />
          </div>

          <Navigation
            mainNavLinks={mainNavLinks}
            isMobile={isMobile}
            isLoading={isLoading}
            userState={userState}
            pricingRequireAuth={pricingRequireAuth}
          />

          <ActionButtons
            isNewYear={isNewYear}
            unreadCount={unreadCount}
            onNoticeOpen={handleNoticeOpen}
            theme={theme}
            onThemeToggle={handleThemeToggle}
            currentLang={currentLang}
            onLanguageChange={handleLanguageChange}
            userState={userState}
            isLoading={isLoading}
            isMobile={isMobile}
            isSelfUseMode={isSelfUseMode}
            logout={logout}
            navigate={navigate}
            t={t}
          />
        </div>
      </div>
    </header>
  );
};

export default HeaderBar;

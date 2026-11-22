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

import { useCallback, useEffect, useRef } from 'react';
import { Modal } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { usePlaygroundNavigationGuard } from './usePlaygroundNavigationGuard';

export const useEditGuard = (dirty, session) => {
  const { t } = useTranslation();
  const pendingRef = useRef(null);
  const mountedRef = useRef(true);

  const settle = useCallback((accepted) => {
    const pending = pendingRef.current;
    if (!pending) return;
    pendingRef.current = null;
    pending.modal?.destroy();
    pending.resolve(accepted && mountedRef.current && session.isCurrent());
  }, [session]);

  useEffect(() => {
    mountedRef.current = true;
    const unsubscribe = session.onInvalidate(() => settle(false));
    return () => {
      mountedRef.current = false;
      unsubscribe();
      settle(false);
    };
  }, [session, settle]);

  // Additional action text and the discard warning share one dialog.
  const confirmDiscard = useCallback((options = {}) => {
    if (!mountedRef.current || !session.isCurrent() || pendingRef.current)
      return Promise.resolve(false);
    if (!dirty && !options.content) return Promise.resolve(true);
    return new Promise((resolve) => {
      const pending = { resolve, modal: null };
      pendingRef.current = pending;
      pending.modal = Modal.confirm({
        title: options.title || t('放弃修改'),
        content: [options.content, dirty && !options.saving ? t('放弃未保存的消息修改？') : '']
          .filter(Boolean).join(' '),
        okText: options.okText || t('确定'),
        cancelText: options.cancelText || t('取消'),
        onOk: () => settle(true),
        onCancel: () => settle(false),
      });
    });
  }, [dirty, session, settle, t]);

  usePlaygroundNavigationGuard(dirty, confirmDiscard, session);

  useEffect(() => {
    if (!dirty) return;
    const protectRefresh = (event) => {
      if (!session.isCurrent()) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', protectRefresh);
    return () => window.removeEventListener('beforeunload', protectRefresh);
  }, [dirty, session]);

  return { confirmDiscard };
};

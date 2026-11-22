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
import i18next from 'i18next';
import { isVerificationRequiredError } from '../../../../helpers/secureApiCall';
import { getSecurityVerificationMethods, verifySecurityAction } from './api';
import { currentSecurityOwner, securityErrorKey } from './securityManagement';

export function useSecurityVerification(owner) {
  const active = useRef(null);
  const retry = useRef(null);
  const locked = useRef(false);
  const [isModalVisible, setOpen] = useState(false);
  const [error, setError] = useState('');
  const [verificationMethods, setMethods] = useState({
    has2FA: false,
    hasPasskey: false,
    passkeySupported: false,
  });
  const [verificationState, setState] = useState({
    method: null,
    code: '',
    loading: false,
  });

  useEffect(() => {
    return () => {
      active.current?.abort();
      active.current = null;
      retry.current = null;
    };
  }, [owner]);

  const isCurrent = (controller) =>
    active.current === controller &&
    !controller.signal.aborted &&
    currentSecurityOwner() === owner;

  const withVerification = async (action) => {
    if (locked.current || !owner || currentSecurityOwner() !== owner)
      return false;
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    retry.current = null;
    locked.current = true;
    setError('');
    try {
      try {
        return await action();
      } catch (cause) {
        if (!isCurrent(controller)) return false;
        if (!isVerificationRequiredError(cause)) throw cause;
      }
      const available = await getSecurityVerificationMethods(
        controller.signal,
        owner,
      );
      if (!isCurrent(controller)) return false;
      setMethods(available);
      const method =
        available.hasPasskey && available.passkeySupported
          ? 'passkey'
          : available.has2FA
            ? '2fa'
            : null;
      if (!method) {
        setError(
          i18next.t('请完成安全验证；未设置验证方式时，请重新登录后重试'),
        );
        return null;
      }
      retry.current = action;
      setState({ method, code: '', loading: false });
      setOpen(true);
      return null;
    } catch (cause) {
      if (isCurrent(controller)) {
        setError(
          i18next.t(
            securityErrorKey(
              cause,
              '请完成安全验证；未设置验证方式时，请重新登录后重试',
            ),
          ),
        );
      }
      return false;
    } finally {
      locked.current = false;
    }
  };

  const executeVerification = async (method, code = '') => {
    const controller = active.current;
    const action = retry.current;
    if (
      !controller ||
      !action ||
      !owner ||
      !isCurrent(controller) ||
      locked.current
    )
      return;
    locked.current = true;
    setState((previous) => ({ ...previous, loading: true }));
    setError('');
    try {
      await verifySecurityAction(method, code, controller.signal, owner);
      if (!isCurrent(controller)) return;
      await action();
      if (!isCurrent(controller)) return;
      retry.current = null;
      setOpen(false);
    } catch (cause) {
      if (isCurrent(controller)) {
        setError(
          i18next.t(
            securityErrorKey(
              cause,
              '请完成安全验证；未设置验证方式时，请重新登录后重试',
            ),
          ),
        );
      }
    } finally {
      locked.current = false;
      if (isCurrent(controller))
        setState((previous) => ({ ...previous, loading: false }));
    }
  };

  const cancelVerification = () => {
    active.current?.abort();
    active.current = null;
    retry.current = null;
    setOpen(false);
    setState({ method: null, code: '', loading: false });
  };

  return {
    isModalVisible,
    error,
    verificationMethods,
    verificationState,
    withVerification,
    executeVerification,
    cancelVerification,
    setVerificationCode: (code) =>
      setState((previous) => ({ ...previous, code })),
    switchVerificationMethod: (method) =>
      setState((previous) => ({ ...previous, method, code: '' })),
  };
}

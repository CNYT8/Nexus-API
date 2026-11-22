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

import { useCallback, useEffect, useRef, useState } from 'react';
import i18next from 'i18next';
import { isVerificationRequiredError } from '../../../../helpers/secureApiCall';

import { currentSecurityOwner, securityErrorKey } from './securityManagement';

export function useSecurityResource(load, owner) {
  const lifetime = useRef(null);
  const sequence = useRef(0);
  const mutation = useRef(false);
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const isCurrent = useCallback(
    (controller) =>
      lifetime.current === controller &&
      !controller.signal.aborted &&
      Boolean(owner) &&
      currentSecurityOwner() === owner,
    [owner],
  );

  const refresh = useCallback(
    async (clearError = true) => {
      const controller = lifetime.current;
      if (!controller || !isCurrent(controller)) return;
      const request = ++sequence.current;
      setLoading(true);
      if (clearError) setError('');
      try {
        const response = await load(controller.signal, owner);
        if (!response.success || response.data === undefined)
          throw new Error('invalid response');
        if (isCurrent(controller) && sequence.current === request)
          setData(response.data);
      } catch (cause) {
        if (isCurrent(controller) && sequence.current === request)
          setError(
            i18next.t(
              securityErrorKey(cause, '加载安全管理数据失败，请刷新重试'),
            ),
          );
      } finally {
        if (isCurrent(controller) && sequence.current === request)
          setLoading(false);
      }
    },
    [isCurrent, load, owner],
  );

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    void refresh();
    return () => {
      controller.abort();
      if (lifetime.current === controller) lifetime.current = null;
    };
  }, [refresh]);

  const run = async (action, accept) => {
    const controller = lifetime.current;
    if (!controller || !isCurrent(controller) || mutation.current) return false;
    mutation.current = true;
    setBusy(true);
    setError('');
    try {
      const response = await action(controller.signal, owner);
      if (!isCurrent(controller)) return false;
      if (!response.success) throw new Error('operation failed');
      accept(response.data);
      await refresh(false);
      return true;
    } catch (cause) {
      if (!isCurrent(controller)) return false;
      if (isVerificationRequiredError(cause)) throw cause;
      setError(
        i18next.t(
          securityErrorKey(cause, '操作失败，已尝试刷新状态，请核对后重试'),
        ),
      );
      await refresh(false);
      return false;
    } finally {
      mutation.current = false;
      if (isCurrent(controller)) setBusy(false);
    }
  };

  return { data, loading, busy, error, refresh, run };
}

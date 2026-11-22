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

import {
  useCallback,
  useContext,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { CanceledError } from 'axios';
import { UserContext } from '../../../context/User';
import { isServerErrorCancelled } from '../../../helpers/server-error-message';

// Share the write lane across source, marketplace, bindings and lifecycle panels.
const mutations = new WeakMap();

export default function useScriptPluginRequests(session) {
  const [userState] = useContext(UserContext);
  const user = useRef(userState.user);
  const mounted = useRef(false);
  const requests = useRef(new Map());
  const [pending, setPending] = useState({});

  useLayoutEffect(() => {
    user.current = userState.user;
  }, [userState.user]);

  useLayoutEffect(() => {
    mounted.current = true;
    const active = requests.current;
    const cancelAll = () => {
      active.forEach((controller) => controller.abort());
      active.clear();
      if (mounted.current) setPending({});
    };
    const unsubscribe = session.onInvalidate(cancelAll);
    return () => {
      mounted.current = false;
      unsubscribe();
      cancelAll();
    };
  }, [session]);

  const cancel = useCallback((slot) => {
    requests.current.get(slot)?.abort();
    requests.current.delete(slot);
    if (mounted.current) {
      setPending((current) => ({ ...current, [slot]: false }));
    }
  }, []);

  const run = useCallback(
    async (slot, action, apply, reject) => {
      const authorized = () =>
        mounted.current &&
        session.isCurrent() &&
        user.current?.role === 100 &&
        String(user.current.id) === session.userId;
      if (!authorized()) return;
      // Serialize writes, including double clicks before React renders again.
      if (slot === 'mutation' && requests.current.has(slot)) return;
      if (slot === 'mutation' && mutations.has(session)) {
        reject(null);
        return;
      }
      requests.current.get(slot)?.abort();
      const controller = new AbortController();
      requests.current.set(slot, controller);
      if (slot === 'mutation') mutations.set(session, controller);
      const current = () =>
        authorized() &&
        !controller.signal.aborted &&
        requests.current.get(slot) === controller;
      setPending((value) => ({ ...value, [slot]: true }));
      try {
        const result = await action({
          signal: controller.signal,
          skipErrorHandler: true,
          disableDuplicate: true,
          headers: {
            'Content-Type': 'application/json',
            'New-Api-User': session.userId,
          },
          // Axios dispatch can run after a cross-tab login but before its event.
          transformRequest: [
            (data, headers) => {
              if (!current()) {
                controller.abort();
                throw new CanceledError('Account session changed');
              }
              headers.set('New-Api-User', session.userId);
              return data === undefined ? undefined : JSON.stringify(data);
            },
          ],
        });
        if (current()) apply(result);
      } catch (cause) {
        if (current() && !isServerErrorCancelled(cause)) reject(cause);
      } finally {
        if (mutations.get(session) === controller) mutations.delete(session);
        if (current()) {
          requests.current.delete(slot);
          setPending((value) => ({ ...value, [slot]: false }));
        }
      }
    },
    [session],
  );

  return { pending, run, cancel };
}

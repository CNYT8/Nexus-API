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

import React, { useEffect, useState } from 'react';
import { Navigate } from 'react-router-dom';
import { API, updateAPI } from './api';
import { history } from './history';
import {
  createSelfRequestGuard,
  getStoredUser,
  shouldApplySelfResponse,
} from './userState';

export function authHeader() {
  // return authorization header with jwt token
  let user = JSON.parse(localStorage.getItem('user'));

  if (user && user.token) {
    return { Authorization: 'Bearer ' + user.token };
  } else {
    return {};
  }
}

export const AuthRedirect = ({ children }) => {
  const user = localStorage.getItem('user');

  if (user) {
    return <Navigate to='/console' replace />;
  }

  return children;
};

function PrivateRoute({ children }) {
  if (!localStorage.getItem('user')) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  return children;
}

export function AdminRoute({ children }) {
  const [checking, setChecking] = useState(false);
  const [authCheck, setAuthCheck] = useState({ key: null, allowed: null });
  const raw = localStorage.getItem('user');
  const localKey = raw || '';
  let hasUser = Boolean(raw);
  let localRole = 0;
  try {
    if (raw) {
      const user = JSON.parse(raw);
      localRole = user?.role || 0;
    }
  } catch (e) {
    // ignore
  }

  useEffect(() => {
    let cancelled = false;
    if (!hasUser || localRole >= 10) return;
    const requestGuard = createSelfRequestGuard();
    setAuthCheck({ key: localKey, allowed: null });
    setChecking(true);
    API.get('/api/user/self', { disableDuplicate: true })
      .then((res) => {
        if (cancelled) return;
        const user = res?.data?.data;
        if (
          res?.data?.success &&
          user &&
          user.role >= 10 &&
          shouldApplySelfResponse(user, requestGuard)
        ) {
          localStorage.setItem('user', JSON.stringify(user));
          updateAPI();
          setAuthCheck({ key: localKey, allowed: true });
        } else {
          setAuthCheck({ key: localKey, allowed: false });
        }
      })
      .catch(() => {
        if (!cancelled) setAuthCheck({ key: localKey, allowed: false });
      })
      .finally(() => {
        if (!cancelled) setChecking(false);
      });
    return () => {
      cancelled = true;
    };
  }, [hasUser, localRole, localKey]);

  const allowed = authCheck.key === localKey ? authCheck.allowed : null;

  if (!hasUser) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  if (localRole >= 10) {
    return children;
  }
  if ((getStoredUser()?.role || 0) >= 10) {
    return children;
  }
  if (allowed === true) {
    return children;
  }
  if (checking || allowed === null) {
    return null;
  }
  return <Navigate to='/forbidden' replace />;
}

export { PrivateRoute };

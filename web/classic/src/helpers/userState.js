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

export const getStoredUser = () => {
  try {
    const raw = localStorage.getItem('user');
    return raw ? JSON.parse(raw) : null;
  } catch (error) {
    return null;
  }
};

export const parseSidebarModules = (value) => {
  if (!value) return null;
  if (typeof value === 'string') {
    if (!value.trim()) return null;
    try {
      return JSON.parse(value);
    } catch (error) {
      return null;
    }
  }
  if (typeof value === 'object') return value;
  return null;
};

export const createSelfRequestGuard = (seq = null) => ({
  key: localStorage.getItem('user'),
  seq,
});

export const shouldApplySelfResponse = (user, guard, latestSeq = null) => {
  if (!user || !guard) return false;
  if (latestSeq !== null && guard.seq !== latestSeq) return false;
  if (localStorage.getItem('user') !== guard.key) return false;

  const currentUser = getStoredUser();
  if (!currentUser) return false;

  if (
    user.id !== undefined &&
    currentUser.id !== undefined &&
    user.id !== currentUser.id
  ) {
    return false;
  }

  return true;
};

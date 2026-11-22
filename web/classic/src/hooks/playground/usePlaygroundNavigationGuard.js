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

import { useContext, useLayoutEffect, useRef } from 'react';
import { UNSAFE_NavigationContext } from 'react-router-dom';
import { installPlaygroundNavigationGuard } from '../../helpers/playgroundNavigation';

export const usePlaygroundNavigationGuard = (dirty, confirmDiscard, session) => {
  const { navigator } = useContext(UNSAFE_NavigationContext);
  const latest = useRef({ dirty, confirmDiscard });
  useLayoutEffect(() => { latest.current = { dirty, confirmDiscard }; }, [dirty, confirmDiscard]);
  useLayoutEffect(() => installPlaygroundNavigationGuard(navigator, {
    isDirty: () => latest.current.dirty,
    confirmDiscard: () => latest.current.confirmDiscard(),
    isCurrent: session.isCurrent,
  }), [navigator, session]);
};

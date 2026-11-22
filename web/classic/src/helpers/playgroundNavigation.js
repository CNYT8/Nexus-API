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

// BrowserRouter 6 exposes a navigator, but its useBlocker needs a data router.
// Keep this adapter local to Playground. POP uses Router's existing idx values;
// restore the original entry before asking, then replay only after acceptance.
export function installPlaygroundNavigationGuard(
  navigator,
  { isDirty, confirmDiscard, isCurrent },
  browserWindow = window,
) {
  const originals = { push: navigator.push, replace: navigator.replace, go: navigator.go };
  let active = true;
  let busy = false;
  let currentIndex = browserWindow.history.state?.idx;
  let pendingPop = null;
  let reconcileTimer = null;
  const mustBlock = () => active && isCurrent() && isDirty();
  const wrapped = {};

  for (const method of ['push', 'replace']) {
    wrapped[method] = (...args) => {
      if (busy || pendingPop) return;
      if (!mustBlock()) {
        originals[method].apply(navigator, args);
        currentIndex = browserWindow.history.state?.idx;
        return;
      }
      busy = true;
      Promise.resolve(confirmDiscard()).then((accepted) => {
        if (accepted && active && isCurrent() && !pendingPop) {
          originals[method].apply(navigator, args);
          currentIndex = browserWindow.history.state?.idx;
        }
      }).catch(() => {}).finally(() => { busy = false; });
    };
    navigator[method] = wrapped[method];
  }
  wrapped.go = (delta) => {
    if (!busy && !pendingPop) originals.go.call(navigator, delta);
  };
  navigator.go = wrapped.go;

  // Coalesce queued POPs before calculating a correction. Issuing a relative
  // go() from every event can make consecutive Back clicks chase each other.
  const reconcile = () => {
    reconcileTimer = null;
    const pending = pendingPop;
    if (!active || !pending) return;
    if (pending.phase === 'replay' && !isCurrent()) pending.phase = 'cancel';
    const actualIndex = browserWindow.history.state?.idx;
    if (!Number.isInteger(actualIndex)) {
      pendingPop = null;
      return;
    }
    const target = pending.phase === 'replay' ? pending.to : pending.from;
    if (actualIndex !== target) {
      originals.go.call(navigator, target - actualIndex);
      return;
    }
    if (pending.phase === 'cancel') {
      pendingPop = null;
      return;
    }
    if (pending.phase !== 'restore') return;
    pending.phase = 'confirm';
    Promise.resolve(busy ? false : confirmDiscard()).catch(() => false).then((accepted) => {
      if (!active || pendingPop !== pending) return;
      pending.phase = accepted && isCurrent() ? 'replay' : 'cancel';
      scheduleReconcile();
    });
  };
  const scheduleReconcile = () => {
    if (reconcileTimer !== null) browserWindow.clearTimeout(reconcileTimer);
    reconcileTimer = browserWindow.setTimeout(reconcile, 0);
  };
  const onPop = (event) => {
    const nextIndex = browserWindow.history.state?.idx;
    if (pendingPop) {
      if (pendingPop.phase === 'replay' && !isCurrent()) pendingPop.phase = 'cancel';
      if (pendingPop.phase === 'replay' && nextIndex === pendingPop.to) {
        pendingPop = null;
        currentIndex = nextIndex;
        return;
      }
      event.stopImmediatePropagation();
      scheduleReconcile();
      return;
    }
    if (mustBlock() && Number.isInteger(currentIndex) && Number.isInteger(nextIndex) && nextIndex !== currentIndex) {
      event.stopImmediatePropagation();
      pendingPop = { from: currentIndex, to: nextIndex, phase: 'restore' };
      scheduleReconcile();
    } else {
      currentIndex = nextIndex;
    }
  };
  browserWindow.addEventListener('popstate', onPop, true);
  return () => {
    active = false;
    pendingPop = null;
    if (reconcileTimer !== null) browserWindow.clearTimeout(reconcileTimer);
    browserWindow.removeEventListener('popstate', onPop, true);
    for (const method of ['push', 'replace', 'go']) {
      if (navigator[method] === wrapped[method]) navigator[method] = originals[method];
    }
  };
}

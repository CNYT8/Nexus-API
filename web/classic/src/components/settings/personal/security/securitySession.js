import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { CanceledError } from 'axios';
import { UserContext } from '../../../../context/User';

// The server validates HttpOnly session cookies. This snapshot prevents work
// started under an old visible account/permission state from being reused.
export function captureSecuritySession(owner) {
  try {
    const user = localStorage.getItem('user');
    const stored = user ? JSON.parse(user) : null;
    if (!owner || stored?.id !== owner ||
      (stored.status !== undefined && stored.status !== 1)) return null;
    return { owner, user };
  } catch {
    return null;
  }
}

export function isSecuritySessionCurrent(snapshot) {
  if (!snapshot) return false;
  return captureSecuritySession(snapshot.owner)?.user === snapshot.user;
}

const sessions = new WeakMap();
export function bindSecuritySession(signal, owner, snapshot = captureSecuritySession(owner), check = () => true) {
  const bound = sessions.get(signal) || { snapshot, check };
  const assertCurrent = () => {
    if (signal.aborted || bound.snapshot?.owner !== owner ||
      !isSecuritySessionCurrent(bound.snapshot) || !bound.check()) {
      throw new CanceledError('Security session changed');
    }
  };
  assertCurrent();
  sessions.set(signal, bound);
  return assertCurrent;
}

export function useSecuritySession(owner) {
  const [state] = useContext(UserContext);
  const contextUser = state?.user;
  const contextRef = useRef(contextUser);
  contextRef.current = contextUser;
  const [snapshot] = useState(() => captureSecuritySession(owner));
  const [contextSnapshot] = useState(() => JSON.stringify(contextUser));
  const [invalid, setInvalid] = useState(false);
  const isCurrent = useCallback(() => !invalid &&
    contextRef.current?.id === owner &&
    JSON.stringify(contextRef.current) === contextSnapshot &&
    isSecuritySessionCurrent(snapshot), [invalid, owner, contextSnapshot, snapshot]);
  useEffect(() => {
    const check = () => { if (!isCurrent()) setInvalid(true); };
    const storage = (event) => {
      if (event.key === null || event.key === 'user') setInvalid(true);
    };
    check();
    window.addEventListener('storage', storage);
    window.addEventListener('focus', check);
    document.addEventListener('visibilitychange', check);
    return () => {
      window.removeEventListener('storage', storage);
      window.removeEventListener('focus', check);
      document.removeEventListener('visibilitychange', check);
    };
  }, [isCurrent, contextUser]);
  return { snapshot, isCurrent, current: isCurrent() };
}

export function copySecuritySecret(value, isCurrent) {
  if (!value || !isCurrent()) return false;
  const field = document.createElement('textarea');
  field.value = value;
  field.readOnly = true;
  field.style.position = 'fixed';
  field.style.opacity = '0';
  document.body.appendChild(field);
  try {
    field.focus();
    field.select();
    field.setSelectionRange(0, value.length);
    return isCurrent() && document.execCommand('copy');
  } finally {
    field.remove();
  }
}

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
import { API, showError, showSuccess } from '../../../../helpers';

const parseValue = (value) => {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};
const isRecord = (value) =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const normalizeValue = (value) => {
  if (Array.isArray(value)) return value.map(normalizeValue);
  if (!isRecord(value)) return value;
  return Object.fromEntries(
    Object.keys(value).sort().map((key) => [key, normalizeValue(value[key])]),
  );
};

export function optionValuesEqual(left, right) {
  return JSON.stringify(normalizeValue(parseValue(left))) ===
    JSON.stringify(normalizeValue(parseValue(right)));
}

function reconcileValue(current, previous, confirmed) {
  if (optionValuesEqual(current, previous)) return confirmed;
  if (isRecord(current) && isRecord(previous) && isRecord(confirmed)) {
    return Object.fromEntries(
      [...new Set([...Object.keys(current), ...Object.keys(confirmed)])]
        .map((key) => [key, reconcileValue(current[key], previous[key], confirmed[key])])
        .filter(([, value]) => value !== undefined),
    );
  }
  return current;
}

export function reconcileOptionDrafts(
  current, previous, confirmed, submitted, successfulKeys = [],
) {
  return Object.fromEntries(
    Object.keys(confirmed).map((key) => {
      const restored = submitted && !successfulKeys.includes(key)
        ? reconcileValue(parseValue(submitted[key]), parseValue(previous[key]), parseValue(confirmed[key]))
        : parseValue(confirmed[key]);
      const value = reconcileValue(
        parseValue(current[key]), parseValue(submitted ? submitted[key] : previous[key]), restored,
      );
      if (optionValuesEqual(value, confirmed[key])) return [key, confirmed[key]];
      if (optionValuesEqual(value, current[key])) return [key, current[key]];
      return [key, typeof current[key] === 'string' ? JSON.stringify(value, null, 2) : value];
    }),
  );
}

export function reconcileModelDrafts(current, previous, confirmed, submitted) {
  const byName = (models) =>
    new Map((models ?? []).map((model) => [model.name, model]));
  const drafts = byName(current);
  const baseline = byName(previous);
  const saved = byName(confirmed);
  const snapshot = byName(submitted);
  const metadata = new Set([
    'name', 'rawRatios', 'lockedCompletionRatio', 'completionRatioLocked', 'hasConflict',
  ]);
  return [...new Set([...saved.keys(), ...drafts.keys()])].flatMap((name) => {
    const draft = drafts.get(name);
    const old = baseline.get(name);
    const next = saved.get(name);
    const sent = snapshot.get(name);
    const reference = submitted ? sent : old;
    if (!draft) return old || sent ? [] : next ? [next] : [];
    if (!next) {
      return !reference || !optionValuesEqual(draft, reference) ? [draft] : [];
    }
    const merged = { ...next };
    Object.keys(draft).forEach((key) => {
      if (metadata.has(key)) return;
      if (!optionValuesEqual(draft[key], reference?.[key])) merged[key] = draft[key];
    });
    return [merged];
  });
}

export function useOptionDrafts(initialValues, options, saving) {
  const [state, setState] = useState(() => ({
    inputs: initialValues,
    baseline: initialValues,
    revision: 0,
  }));
  const submittedRef = useRef(null);
  const defaultsRef = useRef(initialValues);
  const setInputs = (value) => {
    setState((previous) => ({
      ...previous,
      inputs: typeof value === 'function' ? value(previous.inputs) : value,
    }));
  };
  const beginSave = () => {
    submittedRef.current = { inputs: state.inputs, options, successfulKeys: [] };
  };
  const finishSave = ({ successfulKeys }) => {
    if (submittedRef.current) submittedRef.current.successfulKeys = successfulKeys;
  };
  useEffect(() => {
    if (saving) return;
    const submission = submittedRef.current;
    // Keep the snapshot for a later retry when the GET failed.
    if (submission?.options === options) return;
    submittedRef.current = null;
    setState((previous) => {
      const baseline = Object.fromEntries(
        Object.keys(previous.baseline).map((key) => [
          key,
          options[key] ?? defaultsRef.current[key],
        ]),
      );
      const inputs = reconcileOptionDrafts(
        previous.inputs,
        previous.baseline,
        baseline,
        submission?.inputs,
        submission?.successfulKeys,
      );
      return {
        inputs,
        baseline,
        // Do not remount visual editors when only the saved baseline changes.
        revision:
          previous.revision +
          (optionValuesEqual(inputs, previous.inputs) ? 0 : 1),
      };
    });
  }, [options, saving]);
  return { ...state, setInputs, beginSave, finishSave };
}

// Wait for every request before reading back: Promise.all can reject while
// another PUT is still running, making an immediate refresh stale again.
export async function saveOptionBatch(updates, refresh, t, onSettled) {
  const results = await Promise.allSettled(
    updates.map(async ({ key, value }) => {
      const response = await API.put('/api/option/', { key, value });
      if (!response?.data?.success) {
        throw new Error(response?.data?.message || t('保存失败，请重试'));
      }
    }),
  );
  const successfulKeys = updates
    .filter((_, index) => results[index].status === 'fulfilled')
    .map(({ key }) => key);
  onSettled?.({ successfulKeys });
  const failed = results.filter((result) => result.status === 'rejected');
  if (failed.length) {
    showError(
      t('已保存 {{saved}} 项设置，{{failed}} 项失败', {
        saved: results.length - failed.length,
        failed: failed.length,
      }),
    );
  }
  // A rejected request can also have committed before its connection failed.
  let refreshed = false;
  try {
    refreshed = Boolean(await refresh());
  } catch {
    showError(t('刷新失败'));
  }
  if (failed.length === 0 && refreshed) showSuccess(t('保存成功'));
  return failed.length === 0 && refreshed;
}

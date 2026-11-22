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
import { useContext, useEffect, useRef, useState } from 'react';
import i18next from 'i18next';
import { API } from '../../helpers';
import { UserContext } from '../../context/User';

export function confirmedLogCleanupTask(response) {
  const task = response?.data;
  if (
    !response?.success ||
    !Number.isSafeInteger(task?.id) ||
    task.id <= 0 ||
    task.kind !== 'log_cleanup' ||
    !['queued', 'running', 'succeeded', 'failed'].includes(task.status)
  ) {
    throw new Error(response?.message || i18next.t('获取日志清理任务失败'));
  }
  return task;
}

export function useLogCleanupTask() {
  const [userState] = useContext(UserContext);
  const userId = userState.user?.id;
  const storageKey = `log-cleanup-task:${userId}`;
  const [selection, setSelection] = useState(() => {
    let id = 0;
    try {
      id = Number(sessionStorage.getItem(storageKey)) || 0;
    } catch { /* Storage is optional. */ }
    return { userId, id: Number.isSafeInteger(id) && id > 0 ? id : 0 };
  });
  const [result, setResult] = useState({ userId, id: 0, task: null, error: null, fetching: false });
  const [creating, setCreating] = useState(false);
  const creation = useRef(false);
  const [revision, setRevision] = useState(0);
  const taskId = selection.userId === userId ? selection.id : 0;
  const current = result.userId === userId && result.id === taskId ? result : null;

  useEffect(() => {
    if (!taskId) return;
    let disposed = false;
    let timer;
    const controller = new AbortController();
    const pollUntil = Date.now() + 300000;
    const update = (values) => setResult((previous) => ({
      ...(previous.userId === userId && previous.id === taskId ? previous : { task: null }),
      userId,
      id: taskId,
      ...values,
    }));
    const poll = async () => {
      update({ fetching: true });
      try {
        const response = await API.get(`/api/log/cleanup/${taskId}`, {
          signal: controller.signal,
          disableDuplicate: true,
          skipErrorHandler: true,
        });
        const task = confirmedLogCleanupTask(response.data);
        if (disposed) return;
        update({ task, error: null });
        if (['queued', 'running'].includes(task.status) && Date.now() < pollUntil) {
          timer = window.setTimeout(poll, 2000);
        }
      } catch (error) {
        if (!disposed) update({ error });
      } finally {
        if (!disposed) update({ fetching: false });
      }
    };
    void poll();
    return () => {
      disposed = true;
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [taskId, userId, revision]);

  const create = async (targetTimestamp) => {
    if (creation.current) return;
    creation.current = true;
    setCreating(true);
    try {
      const response = await API.post('/api/log/cleanup', null, {
        params: { target_timestamp: targetTimestamp },
        skipErrorHandler: true,
      });
      const task = confirmedLogCleanupTask(response.data);
      setResult({ userId, id: task.id, task, error: null, fetching: false });
      setSelection({ userId, id: task.id });
      try {
        sessionStorage.setItem(storageKey, String(task.id));
      } catch { /* Storage is optional. */ }
      return task;
    } finally {
      creation.current = false;
      setCreating(false);
    }
  };

  const active = taskId > 0 && (!current?.task || ['queued', 'running'].includes(current.task.status));
  return {
    taskId,
    task: current?.task,
    error: current?.error,
    fetching: current?.fetching || false,
    active,
    creating,
    create,
    refresh: () => setRevision((value) => value + 1),
  };
}

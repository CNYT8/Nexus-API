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

import { useCallback, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import {
  API,
  processModelsData,
  processGroupsData,
  showError,
} from '../../helpers';
import { API_ENDPOINTS } from '../../constants/playground.constants';

export const useDataLoader = (
  userState,
  inputs,
  handleInputChange,
  setModels,
  setGroups,
  session,
) => {
  const { t } = useTranslation();
  const availableModels = useRef(null);
  const isModelAvailable = useCallback((model, group) => {
    const loaded = availableModels.current;
    return session.isCurrent() && loaded?.group === group &&
      loaded.models.some((option) => option.value === model);
  }, [session]);

  const loadModels = useCallback(
    async (signal) => {
      if (!session.isCurrent() || signal.aborted) return;
      try {
        availableModels.current = null;
        setModels([]);
        const res = await API.get(API_ENDPOINTS.USER_MODELS, {
          signal,
          params: { group: inputs.group },
          headers: { 'New-Api-User': session.userId },
          disableDuplicate: true,
          skipErrorHandler: true,
        });
        if (!session.isCurrent() || signal.aborted) return;
        const { success, message, data } = res.data;

        if (success) {
          const { modelOptions, selectedModel } = processModelsData(
            data,
            inputs.model,
          );
          availableModels.current = { group: inputs.group, models: modelOptions };
          setModels(modelOptions);

          const nextModel = modelOptions.length ? selectedModel : '';
          if (nextModel !== inputs.model) {
            handleInputChange('model', nextModel);
          }
        } else {
          showError(t(message));
        }
      } catch (error) {
        if (session.isCurrent() && !signal.aborted)
          showError(t('加载模型失败'));
      }
    },
    [inputs.model, inputs.group, handleInputChange, setModels, t, session],
  );

  const loadGroups = useCallback(
    async (signal) => {
      if (!session.isCurrent() || signal.aborted) return;
      try {
        const res = await API.get(API_ENDPOINTS.USER_GROUPS, {
          signal,
          headers: { 'New-Api-User': session.userId },
          disableDuplicate: true,
          skipErrorHandler: true,
        });
        if (!session.isCurrent() || signal.aborted) return;
        const { success, message, data } = res.data;

        if (success) {
          const storedUser = JSON.parse(localStorage.getItem('user') || 'null');
          const userGroup =
            String(userState?.user?.id) === session.userId
              ? userState.user.group
              : storedUser?.group;
          const groupOptions = processGroupsData(data, userGroup);
          setGroups(groupOptions);

          const hasCurrentGroup = groupOptions.some(
            (option) => option.value === inputs.group,
          );
          if (!hasCurrentGroup) {
            handleInputChange('group', groupOptions[0]?.value || '');
          }
        } else {
          showError(t(message));
        }
      } catch (error) {
        if (session.isCurrent() && !signal.aborted)
          showError(t('加载分组失败'));
      }
    },
    [userState, inputs.group, handleInputChange, setGroups, t, session],
  );

  // 自动加载数据
  useEffect(() => {
    if (!session.isCurrent()) return;
    const controller = new AbortController();
    const unsubscribe = session.onInvalidate(() => controller.abort());
    loadModels(controller.signal);
    return () => {
      unsubscribe();
      controller.abort();
    };
  }, [session, loadModels]);

  useEffect(() => {
    if (!session.isCurrent()) return;
    const controller = new AbortController();
    const unsubscribe = session.onInvalidate(() => controller.abort());
    loadGroups(controller.signal);
    return () => {
      unsubscribe();
      controller.abort();
    };
  }, [session, loadGroups]);

  return {
    loadModels,
    loadGroups,
    isModelAvailable,
  };
};

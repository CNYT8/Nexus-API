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

import { useState, useCallback, useRef, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import {
  getDefaultMessages,
  DEFAULT_CONFIG,
  DEBUG_TABS,
  MESSAGE_STATUS,
} from '../../constants/playground.constants';
import {
  loadConfig,
  saveConfig,
  loadMessages,
  saveMessages,
  clearMessages,
} from '../../components/playground/configStorage';
import { processIncompleteThinkTags } from '../../helpers';
import { restoreMessages } from '../../helpers/playgroundMessages';

const finalizePendingMessages = (messages) => {
  let changed = false;
  const result = messages.map((message) => {
    if (message.role !== 'assistant' || ![MESSAGE_STATUS.LOADING, MESSAGE_STATUS.INCOMPLETE].includes(message.status)) return message;
    changed = true;
    const processed = typeof message.content === 'string'
      ? processIncompleteThinkTags(message.content, message.reasoningContent || '')
      : { content: message.content, reasoningContent: message.reasoningContent };
    return {
      ...message,
      content: processed.content,
      reasoningContent: processed.reasoningContent || null,
      status: MESSAGE_STATUS.COMPLETE,
      isThinkingComplete: true,
    };
  });
  return changed ? result : messages;
};

export const usePlaygroundState = (session) => {
  const { t } = useTranslation();

  // 使用惰性初始化，确保只在组件首次挂载时加载配置和消息
  const [savedConfig] = useState(() => loadConfig(session.userId));
  const [initialMessages] = useState(() => {
    const loaded = loadMessages(session.userId);
    // 检查是否是旧的中文默认消息，如果是则清除
    if (
      loaded &&
      loaded.length === 2 &&
      loaded[0].id === '2' &&
      loaded[1].id === '3'
    ) {
      const hasOldChinese =
        loaded[0].content === '你好' ||
        loaded[1].content === '你好，请问有什么可以帮助您的吗？' ||
        loaded[1].content === '你好！很高兴见到你。有什么我可以帮助你的吗？';

      if (hasOldChinese) {
        // 清除旧的默认消息
        clearMessages(session.userId);
        return null;
      }
    }
    return loaded;
  });

  // 基础配置状态
  const [inputs, setInputs] = useState(
    savedConfig.inputs || DEFAULT_CONFIG.inputs,
  );
  const [parameterEnabled, setParameterEnabled] = useState(
    savedConfig.parameterEnabled || DEFAULT_CONFIG.parameterEnabled,
  );
  const [showDebugPanel, setShowDebugPanel] = useState(
    savedConfig.showDebugPanel || DEFAULT_CONFIG.showDebugPanel,
  );
  const [customRequestMode, setCustomRequestMode] = useState(
    savedConfig.customRequestMode || DEFAULT_CONFIG.customRequestMode,
  );
  const [customRequestBody, setCustomRequestBody] = useState(
    savedConfig.customRequestBody || DEFAULT_CONFIG.customRequestBody,
  );

  // UI状态
  const [showSettings, setShowSettings] = useState(false);
  const [models, setModels] = useState([]);
  const [groups, setGroups] = useState([]);
  const [status, setStatus] = useState({});

  // 消息相关状态 - 使用加载的消息或默认消息初始化
  const [message, setMessageState] = useState(
    () => initialMessages || getDefaultMessages(t),
  );

  // 调试状态
  const [debugData, setDebugData] = useState({
    request: null,
    response: null,
    timestamp: null,
    previewRequest: null,
    previewTimestamp: null,
  });
  const [activeDebugTab, setActiveDebugTab] = useState(DEBUG_TABS.PREVIEW);
  const [previewPayload, setPreviewPayload] = useState(null);

  // 编辑状态
  const [editingMessageId, setEditingMessageId] = useState(null);
  const [editValue, setEditValue] = useState('');

  // Refs
  const sseSourceRef = useRef(null);
  const chatRef = useRef(null);
  const saveConfigTimeoutRef = useRef(null);
  const messageRef = useRef(message);
  const saveMessagesTimerRef = useRef(null);
  const messagesDirtyRef = useRef(false);
  const mountedRef = useRef(true);

  // 同步提交并保存同一份快照，避免 React updater 重放或延时保存覆盖终态。
  const setMessage = useCallback(
    (updater) => {
      if (!mountedRef.current || !session.isCurrent()) return;
      const next =
        typeof updater === 'function' ? updater(messageRef.current) : updater;
      if (next === messageRef.current) return;
      messageRef.current = next;
      setMessageState(next);
      messagesDirtyRef.current = true;
      const streaming = next.some((m) => ['loading', 'incomplete'].includes(m.status));
      if (!streaming) {
        clearTimeout(saveMessagesTimerRef.current);
        saveMessagesTimerRef.current = null;
        messagesDirtyRef.current = !saveMessages(next, session.userId);
      } else if (!saveMessagesTimerRef.current) {
        saveMessagesTimerRef.current = setTimeout(() => {
          saveMessagesTimerRef.current = null;
          if (!mountedRef.current || !session.isCurrent()) return;
          messagesDirtyRef.current = !saveMessages(messageRef.current, session.userId);
        }, 500);
      }
    },
    [session],
  );
  const getMessages = useCallback(() => messageRef.current, []);

  // 语言切换只更新仍未使用的示例，不能覆盖本次新建的对话。
  useEffect(() => {
    const current = messageRef.current;
    if (
      !initialMessages &&
      current.length === 2 &&
      current[0].id === '2' &&
      current[1].id === '3'
    ) {
      setMessage(getDefaultMessages(t));
    }
  }, [t, initialMessages, setMessage]);

  // 配置更新函数
  const handleInputChange = useCallback(
    (name, value) => {
      if (!mountedRef.current || !session.isCurrent()) return;
      setInputs((prev) => ({ ...prev, [name]: value }));
    },
    [session],
  );

  const handleParameterToggle = useCallback(
    (paramName) => {
      if (!mountedRef.current || !session.isCurrent()) return;
      setParameterEnabled((prev) => ({
        ...prev,
        [paramName]: !prev[paramName],
      }));
    },
    [session],
  );

  // 配置保存
  const debouncedSaveConfig = useCallback(() => {
    if (saveConfigTimeoutRef.current) {
      clearTimeout(saveConfigTimeoutRef.current);
    }

    saveConfigTimeoutRef.current = setTimeout(() => {
      const configToSave = {
        inputs,
        parameterEnabled,
        showDebugPanel,
        customRequestMode,
        customRequestBody,
      };
      if (mountedRef.current && session.isCurrent())
        saveConfig(configToSave, session.userId);
    }, 1000);
  }, [
    inputs,
    parameterEnabled,
    showDebugPanel,
    customRequestMode,
    customRequestBody,
    session,
  ]);

  // 配置导入/重置
  const handleConfigImport = useCallback(
    (importedConfig) => {
      if (!mountedRef.current || !session.isCurrent()) return;
      if (importedConfig.inputs) {
        const parsedMaxTokens = parseInt(importedConfig.inputs.max_tokens, 10);
        setInputs((prev) => ({
          ...prev,
          ...importedConfig.inputs,
          max_tokens: Number.isNaN(parsedMaxTokens)
            ? importedConfig.inputs.max_tokens
            : parsedMaxTokens,
        }));
      }
      if (importedConfig.parameterEnabled) {
        setParameterEnabled((prev) => ({
          ...prev,
          ...importedConfig.parameterEnabled,
        }));
      }
      if (typeof importedConfig.showDebugPanel === 'boolean') {
        setShowDebugPanel(importedConfig.showDebugPanel);
      }
      if (importedConfig.customRequestMode) {
        setCustomRequestMode(importedConfig.customRequestMode);
      }
      if (importedConfig.customRequestBody) {
        setCustomRequestBody(importedConfig.customRequestBody);
      }
      // 如果导入的配置包含消息，也恢复消息
      if (importedConfig.messages && Array.isArray(importedConfig.messages)) {
        setMessage(restoreMessages(importedConfig.messages));
      }
    },
    [session, setMessage],
  );

  const handleConfigReset = useCallback(
    (options = {}) => {
      if (!mountedRef.current || !session.isCurrent()) return;
      const { resetMessages = false } = options;

      setInputs(DEFAULT_CONFIG.inputs);
      setParameterEnabled(DEFAULT_CONFIG.parameterEnabled);
      setShowDebugPanel(DEFAULT_CONFIG.showDebugPanel);
      setCustomRequestMode(DEFAULT_CONFIG.customRequestMode);
      setCustomRequestBody(DEFAULT_CONFIG.customRequestBody);

      // 只有在明确指定时才重置消息
      if (resetMessages) {
        setMessage(getDefaultMessages(t));
      }
    },
    [session, setMessage, t],
  );

  useEffect(() => {
    mountedRef.current = true;
    let finalized = false;
    const finalize = () => {
      if (finalized) return;
      finalized = true;
      clearTimeout(saveConfigTimeoutRef.current);
      clearTimeout(saveMessagesTimerRef.current);
      saveMessagesTimerRef.current = null;
      const saved = finalizePendingMessages(messageRef.current);
      if (messagesDirtyRef.current || saved !== messageRef.current) {
        messageRef.current = saved;
        messagesDirtyRef.current = !saveMessages(saved, session.userId);
      }
    };
    const persist = () => {
      if (!session.isCurrent() || finalized || !messagesDirtyRef.current) return;
      clearTimeout(saveMessagesTimerRef.current);
      saveMessagesTimerRef.current = null;
      messagesDirtyRef.current = !saveMessages(messageRef.current, session.userId);
    };
    window.addEventListener('pagehide', persist);
    window.addEventListener('beforeunload', persist);
    const unsubscribe = session.onInvalidate(finalize);
    return () => {
      window.removeEventListener('pagehide', persist);
      window.removeEventListener('beforeunload', persist);
      mountedRef.current = false;
      unsubscribe();
      finalize();
    };
  }, [session]);

  // 页面首次加载时，若最后一条消息仍处于 LOADING/INCOMPLETE 状态，自动修复
  useEffect(() => {
    setMessage(finalizePendingMessages(messageRef.current));
  }, []);

  return {
    // 配置状态
    inputs,
    parameterEnabled,
    showDebugPanel,
    customRequestMode,
    customRequestBody,

    // UI状态
    showSettings,
    models,
    groups,
    status,

    // 消息状态
    message,

    // 调试状态
    debugData,
    activeDebugTab,
    previewPayload,

    // 编辑状态
    editingMessageId,
    editValue,

    // Refs
    sseSourceRef,
    chatRef,
    saveConfigTimeoutRef,

    // 更新函数
    setInputs,
    setParameterEnabled,
    setShowDebugPanel,
    setCustomRequestMode,
    setCustomRequestBody,
    setShowSettings,
    setModels,
    setGroups,
    setStatus,
    setMessage,
    setDebugData,
    setActiveDebugTab,
    setPreviewPayload,
    setEditingMessageId,
    setEditValue,

    // 处理函数
    handleInputChange,
    handleParameterToggle,
    debouncedSaveConfig,
    getMessages,
    handleConfigImport,
    handleConfigReset,
  };
};

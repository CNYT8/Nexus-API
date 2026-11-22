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

import React, { useContext, useEffect, useCallback, useLayoutEffect, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Toast } from '@douyinfe/semi-ui';

// Context
import { UserContext } from '../../context/User';
import { useIsMobile } from '../../hooks/common/useIsMobile';

// hooks
import { usePlaygroundState } from '../../hooks/playground/usePlaygroundState';
import { useAccountSession } from '../../hooks/playground/accountSession';
import { useMessageActions } from '../../hooks/playground/useMessageActions';
import { useApiRequest } from '../../hooks/playground/useApiRequest';
import { useSyncMessageAndCustomBody } from '../../hooks/playground/useSyncMessageAndCustomBody';
import { useMessageEdit } from '../../hooks/playground/useMessageEdit';
import { useDataLoader } from '../../hooks/playground/useDataLoader';

// Constants and utils
import {
  MESSAGE_ROLES,
  ERROR_MESSAGES,
} from '../../constants/playground.constants';
import {
  getLogo,
  stringToColor,
  buildMessageContent,
  createMessage,
  createLoadingAssistantMessage,
  getTextContent,
  buildApiPayload,
  encodeToBase64,
} from '../../helpers';

// Components
import {
  OptimizedMessageContent,
  OptimizedMessageActions,
} from '../../components/playground/OptimizedComponents';
import ChatArea from '../../components/playground/ChatArea';
import { PlaygroundProvider } from '../../contexts/PlaygroundContext';

// 生成头像
const generateAvatarDataUrl = (username) => {
  if (!username) {
    return 'https://lf3-static.bytednsdoc.com/obj/eden-cn/ptlz_zlp/ljhwZthlaukjlkulzlp/docs-icon.png';
  }
  const firstLetter = username[0].toUpperCase();
  const bgColor = stringToColor(username);
  const svg = `
    <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32">
      <circle cx="16" cy="16" r="16" fill="${bgColor}" />
      <text x="50%" y="50%" dominant-baseline="central" text-anchor="middle" font-size="16" fill="#ffffff" font-family="sans-serif">${firstLetter}</text>
    </svg>
  `;
  return `data:image/svg+xml;base64,${encodeToBase64(svg)}`;
};

const Playground = () => {
  const session = useAccountSession();
  return session.userId ? (
    <PlaygroundSession key={session.revision} session={session} />
  ) : null;
};

const PlaygroundSession = ({ session }) => {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const isMobile = useIsMobile();
  const styleState = { isMobile };
  const [searchParams] = useSearchParams();

  const state = usePlaygroundState(session);
  const {
    inputs,
    parameterEnabled,
    showDebugPanel,
    customRequestMode,
    customRequestBody,
    showSettings,
    models,
    groups,
    status,
    message,
    debugData,
    activeDebugTab,
    previewPayload,
    sseSourceRef,
    chatRef,
    handleInputChange,
    handleParameterToggle,
    debouncedSaveConfig,
    getMessages,
    handleConfigImport,
    handleConfigReset,
    setShowSettings,
    setModels,
    setGroups,
    setStatus,
    setMessage,
    setDebugData,
    setActiveDebugTab,
    setPreviewPayload,
    setShowDebugPanel,
    setCustomRequestMode,
    setCustomRequestBody,
  } = state;

  // API 请求相关
  const { sendRequest, onStopGenerator, canSend } = useApiRequest(
    setMessage,
    setDebugData,
    setActiveDebugTab,
    sseSourceRef,
    session,
  );

  // 数据加载
  const { isModelAvailable } = useDataLoader(
    userState,
    inputs,
    handleInputChange,
    setModels,
    setGroups,
    session,
  );

  // Re-read settings after confirmation, before constructing a request.
  const sendCurrent = useCallback((payload, stream, messages) => {
    const group = payload.group ?? inputs.group;
    if (group !== inputs.group || !isModelAvailable(payload.model, group)) return false;
    return sendRequest(payload, stream, messages);
  }, [inputs.group, isModelAvailable, sendRequest]);
  const latestSend = useRef(null);
  useLayoutEffect(() => {
    latestSend.current = { sendCurrent, inputs, parameterEnabled, sendMessage };
  });
  const submitEdited = useCallback((messages) => {
    const current = latestSend.current;
    const payload = buildApiPayload(messages, null, current.inputs, current.parameterEnabled);
    return current.sendCurrent(payload, current.inputs.stream, [...messages, createLoadingAssistantMessage()]);
  }, []);

  // 消息编辑
  const {
    editingMessageId,
    editValue,
    setEditValue,
    handleMessageEdit,
    handleEditSave,
    handleEditCancel,
    requestAction,
  } = useMessageEdit(
    setMessage,
    submitEdited,
    canSend,
    getMessages,
    session,
  );

  // 消息和自定义请求体同步
  const { syncMessageToCustomBody, syncCustomBodyToMessage } =
    useSyncMessageAndCustomBody(
      customRequestMode,
      customRequestBody,
      message,
      inputs,
      setCustomRequestBody,
      setMessage,
      debouncedSaveConfig,
      canSend,
    );

  // 角色信息
  const roleInfo = {
    user: {
      name: userState?.user?.username || 'User',
      avatar: generateAvatarDataUrl(userState?.user?.username),
    },
    assistant: {
      name: 'Assistant',
      avatar: getLogo(),
    },
    system: {
      name: 'System',
      avatar: getLogo(),
    },
  };

  // 消息操作
  const messageActions = useMessageActions(
    message,
    setMessage,
    (content, attachment, history) => latestSend.current.sendMessage(content, attachment, history),
    canSend,
    getMessages,
    requestAction,
  );

  // 构建预览请求体
  const constructPreviewPayload = useCallback(() => {
    try {
      // 如果是自定义请求体模式且有自定义内容，直接返回解析后的自定义请求体
      if (customRequestMode && customRequestBody && customRequestBody.trim()) {
        try {
          return JSON.parse(customRequestBody);
        } catch (parseError) {
          console.warn('自定义请求体JSON解析失败，回退到默认预览:', parseError);
        }
      }

      // 默认预览逻辑
      let messages = [...message];

      // 如果存在用户消息
      if (
        !(
          messages.length === 0 ||
          messages.every((msg) => msg.role !== MESSAGE_ROLES.USER)
        )
      ) {
        // 处理最后一个用户消息的图片
        for (let i = messages.length - 1; i >= 0; i--) {
          if (messages[i].role === MESSAGE_ROLES.USER) {
            if (inputs.imageUrls) {
              const validImageUrls = inputs.imageUrls.filter(
                (url) => url && url.trim() !== '',
              );
              if (validImageUrls.length > 0) {
                const textContent = getTextContent(messages[i]) || '示例消息';
                const content = buildMessageContent(
                  textContent,
                  validImageUrls,
                  true,
                );
                messages[i] = { ...messages[i], content };
              }
            }
            break;
          }
        }
      }

      return buildApiPayload(messages, null, inputs, parameterEnabled);
    } catch (error) {
      console.error('构造预览请求体失败:', error);
      return null;
    }
  }, [inputs, parameterEnabled, message, customRequestMode, customRequestBody]);

  // 发送消息
  function onMessageSend(content, attachment) {
    return requestAction(() => latestSend.current.sendMessage(content, attachment));
  }

  function sendMessage(content, attachment, history) {
    if (!canSend()) return false;
    const previous = history || getMessages();

    // 创建用户消息和加载消息
    const userMessage = createMessage(MESSAGE_ROLES.USER, content);
    const loadingMessage = createLoadingAssistantMessage();

    // 如果是自定义请求体模式
    if (customRequestMode && customRequestBody) {
      try {
        const customPayload = JSON.parse(customRequestBody);

        return sendCurrent(customPayload, customPayload.stream !== false, [
          ...previous,
          ...(!history ? [userMessage] : []),
          loadingMessage,
        ]);
      } catch (error) {
        console.error('自定义请求体JSON解析失败:', error);
        Toast.error(ERROR_MESSAGES.JSON_PARSE_ERROR);
        return false;
      }
    }

    // 默认模式：只要存在有效图片就直接附加，不再用 imageEnabled 做门控
    const validImageUrls = inputs.imageUrls.filter(
      (url) => url && url.trim() !== '',
    );
    const messageContent = buildMessageContent(
      content,
      validImageUrls,
      validImageUrls.length > 0,
    );
    const userMessageWithImages = createMessage(
      MESSAGE_ROLES.USER,
      messageContent,
    );

    const newMessages = history
      ? previous
      : [...previous, userMessageWithImages];
    const payload = buildApiPayload(
      newMessages,
      null,
      inputs,
      parameterEnabled,
    );
    if (!sendCurrent(payload, inputs.stream, [...newMessages, loadingMessage]))
      return false;

    // 发送后立即清空输入框中已附加的图片缩略图，避免下一轮对话重复携带
    if (!history && validImageUrls.length > 0) {
      handleInputChange('imageUrls', []);
    }
    return true;
  }

  // 切换推理展开状态
  const toggleReasoningExpansion = useCallback(
    (messageId) => {
      setMessage((prevMessages) =>
        prevMessages.map((msg) =>
          msg.id === messageId && msg.role === MESSAGE_ROLES.ASSISTANT
            ? { ...msg, isReasoningExpanded: !msg.isReasoningExpanded }
            : msg,
        ),
      );
    },
    [setMessage],
  );

  // 渲染函数
  const renderCustomChatContent = useCallback(
    ({ message, className }) => {
      const isCurrentlyEditing = editingMessageId === message.id;

      return (
        <OptimizedMessageContent
          message={message}
          className={className}
          styleState={styleState}
          onToggleReasoningExpansion={toggleReasoningExpansion}
          isEditing={isCurrentlyEditing}
          onEditSave={() => handleEditSave(false)}
          onEditSaveAndSubmit={() => handleEditSave(true)}
          onEditCancel={handleEditCancel}
          editValue={editValue}
          onEditValueChange={setEditValue}
        />
      );
    },
    [
      styleState,
      editingMessageId,
      editValue,
      handleEditSave,
      handleEditCancel,
      setEditValue,
      toggleReasoningExpansion,
    ],
  );

  const renderChatBoxAction = useCallback(
    (props) => {
      const { message: currentMessage } = props;
      const isAnyMessageGenerating = message.some(
        (msg) => msg.status === 'loading' || msg.status === 'incomplete',
      );
      const isCurrentlyEditing = editingMessageId === currentMessage.id;

      return (
        <OptimizedMessageActions
          message={currentMessage}
          styleState={styleState}
          onMessageReset={messageActions.handleMessageReset}
          onMessageCopy={messageActions.handleMessageCopy}
          onMessageDelete={messageActions.handleMessageDelete}
          onRoleToggle={messageActions.handleRoleToggle}
          onMessageEdit={handleMessageEdit}
          isAnyMessageGenerating={isAnyMessageGenerating}
          isEditing={isCurrentlyEditing}
        />
      );
    },
    [messageActions, styleState, message, editingMessageId, handleMessageEdit],
  );

  // Effects

  // 同步消息和自定义请求体
  useEffect(() => {
    syncMessageToCustomBody();
  }, [message, syncMessageToCustomBody]);

  useEffect(() => {
    syncCustomBodyToMessage();
  }, [customRequestBody, syncCustomBodyToMessage]);

  // 处理URL参数
  useEffect(() => {
    if (searchParams.get('expired')) {
      Toast.warning(t('登录过期，请重新登录！'));
    }
  }, [searchParams, t]);

  // Playground 组件无需再监听窗口变化，isMobile 由 useIsMobile Hook 自动更新

  // 构建预览payload
  useEffect(() => {
    const timer = setTimeout(() => {
      const preview = constructPreviewPayload();
      setPreviewPayload(preview);
      setDebugData((prev) => ({
        ...prev,
        previewRequest: preview ? JSON.stringify(preview, null, 2) : null,
        previewTimestamp: preview ? new Date().toISOString() : null,
      }));
    }, 300);

    return () => clearTimeout(timer);
  }, [
    message,
    inputs,
    parameterEnabled,
    customRequestMode,
    customRequestBody,
    constructPreviewPayload,
    setPreviewPayload,
    setDebugData,
  ]);

  // 自动保存配置
  useEffect(() => {
    debouncedSaveConfig();
  }, [
    inputs,
    parameterEnabled,
    showDebugPanel,
    customRequestMode,
    customRequestBody,
    debouncedSaveConfig,
  ]);

  // 清空对话的处理函数
  const handleClearMessages = useCallback(() => requestAction(() => {
    onStopGenerator();
    setMessage([]);
    return true;
  }, {
    title: t('清空对话'),
    content: t('确定清空所有消息吗？此操作无法撤销。'),
  }), [setMessage, onStopGenerator, requestAction, t]);

  // 处理粘贴图片：不再受不可达的 imageEnabled 开关限制
  const handlePasteImage = useCallback(
    (base64Data) => {
      const newUrls = [...(inputs.imageUrls || []), base64Data];
      handleInputChange('imageUrls', newUrls);
    },
    [inputs.imageUrls, handleInputChange],
  );

  const isAnyMessageGenerating = message.some(
    (msg) => msg.status === 'loading' || msg.status === 'incomplete',
  );

  // Playground Context 值
  const playgroundContextValue = {
    onPasteImage: handlePasteImage,
    imageUrls: inputs.imageUrls || [],
    imageEnabled: inputs.imageEnabled || false,
    session,
    canSend,
  };

  return (
    <PlaygroundProvider value={playgroundContextValue}>
      <div className='relative flex flex-col w-full h-[calc(100vh-64px)] mt-[64px] overflow-hidden'>
        <ChatArea
          chatRef={chatRef}
          message={message}
          inputs={inputs}
          parameterEnabled={parameterEnabled}
          customRequestMode={customRequestMode}
          models={models}
          groups={groups}
          styleState={styleState}
          isGenerating={isAnyMessageGenerating}
          showDebugPanel={showDebugPanel}
          roleInfo={roleInfo}
          onInputChange={handleInputChange}
          onParameterToggle={handleParameterToggle}
          onMessageSend={onMessageSend}
          onMessageCopy={messageActions.handleMessageCopy}
          onMessageReset={messageActions.handleMessageReset}
          onMessageDelete={messageActions.handleMessageDelete}
          onStopGenerator={onStopGenerator}
          onClearMessages={handleClearMessages}
          onToggleDebugPanel={() => setShowDebugPanel(!showDebugPanel)}
          renderCustomChatContent={renderCustomChatContent}
          renderChatBoxAction={renderChatBoxAction}
        />
      </div>
    </PlaygroundProvider>
  );
};

export default Playground;

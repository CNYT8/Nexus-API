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
import { SSE } from 'sse.js';
import { createStreamTextAccumulator } from '../../helpers/playgroundStream';
import {
  API_ENDPOINTS,
  MESSAGE_STATUS,
  DEBUG_TABS,
} from '../../constants/playground.constants';
import {
  handleApiError,
  processThinkTags,
  processIncompleteThinkTags,
} from '../../helpers';

export const useApiRequest = (
  setMessage,
  setDebugData,
  setActiveDebugTab,
  sseSourceRef,
  session,
) => {
  const { t } = useTranslation();
  const activeRequestRef = useRef(null);
  const mountedRef = useRef(true);
  const isActive = useCallback(
    (request) =>
      mountedRef.current &&
      activeRequestRef.current === request &&
      session.isCurrent(),
    [session],
  );
  const canSend = useCallback(
    () =>
      mountedRef.current && session.isCurrent() && !activeRequestRef.current,
    [session],
  );
  const release = useCallback(
    (request) => {
      if (activeRequestRef.current !== request) return;
      request.flush?.();
      request.cancel?.();
      activeRequestRef.current = null;
      if (sseSourceRef.current === request.source) sseSourceRef.current = null;
      request.controller.abort();
      request.source?.close();
    },
    [sseSourceRef],
  );
  useEffect(() => {
    mountedRef.current = true;
    const cancel = () => {
      if (activeRequestRef.current) release(activeRequestRef.current);
    };
    const unsubscribe = session.onInvalidate(cancel);
    return () => {
      mountedRef.current = false;
      unsubscribe();
      cancel();
    };
  }, [session, release]);

  // 处理消息自动关闭逻辑的公共函数
  const applyAutoCollapseLogic = useCallback(
    (message, isThinkingComplete = true) => {
      const shouldAutoCollapse =
        isThinkingComplete && !message.hasAutoCollapsed;
      return {
        isThinkingComplete,
        hasAutoCollapsed: shouldAutoCollapse || message.hasAutoCollapsed,
        isReasoningExpanded: shouldAutoCollapse
          ? false
          : message.isReasoningExpanded,
      };
    },
    [],
  );

  // 流式消息更新
  const streamMessageUpdate = useCallback(
    (textChunk, type) => {
      setMessage((prevMessage) => {
        const lastMessage = prevMessage[prevMessage.length - 1];
        if (!lastMessage) return prevMessage;
        if (lastMessage.role !== 'assistant') return prevMessage;
        if (lastMessage.status === MESSAGE_STATUS.ERROR) {
          return prevMessage;
        }

        if (
          lastMessage.status === MESSAGE_STATUS.LOADING ||
          lastMessage.status === MESSAGE_STATUS.INCOMPLETE
        ) {
          let newMessage = { ...lastMessage };

          if (type === 'reasoning') {
            newMessage = {
              ...newMessage,
              reasoningContent:
                (lastMessage.reasoningContent || '') + textChunk,
              status: MESSAGE_STATUS.INCOMPLETE,
              isThinkingComplete: false,
            };
          } else if (type === 'content') {
            const newContent = (lastMessage.content || '') + textChunk;
            let thinkingCompleteFromTags = lastMessage.isThinkingComplete;

            if (
              lastMessage.isReasoningExpanded &&
              newContent.includes('</think>')
            ) {
              const thinkMatches = newContent.match(/<think>/g);
              const thinkCloseMatches = newContent.match(/<\/think>/g);
              if (
                thinkMatches &&
                thinkCloseMatches &&
                thinkCloseMatches.length >= thinkMatches.length
              ) {
                thinkingCompleteFromTags = true; // think标签闭合也标记思考完成
              }
            }

            // 如果开始接收content内容，且之前有reasoning内容，或者think标签已闭合，则标记思考完成
            const isThinkingComplete =
              (lastMessage.reasoningContent &&
                !lastMessage.isThinkingComplete) ||
              thinkingCompleteFromTags;

            const autoCollapseState = applyAutoCollapseLogic(
              lastMessage,
              isThinkingComplete,
            );

            newMessage = {
              ...newMessage,
              content: newContent,
              status: MESSAGE_STATUS.INCOMPLETE,
              ...autoCollapseState,
            };
          }

          return [...prevMessage.slice(0, -1), newMessage];
        }

        return prevMessage;
      });
    },
    [setMessage, applyAutoCollapseLogic],
  );

  // 完成消息
  const completeMessage = useCallback(
    (status = MESSAGE_STATUS.COMPLETE) => {
      setMessage((prevMessage) => {
        const lastMessage = prevMessage[prevMessage.length - 1];
        if (
          !lastMessage ||
          lastMessage.status === MESSAGE_STATUS.COMPLETE ||
          lastMessage.status === MESSAGE_STATUS.ERROR
        ) {
          return prevMessage;
        }

        const autoCollapseState = applyAutoCollapseLogic(lastMessage, true);

        const updatedMessages = [
          ...prevMessage.slice(0, -1),
          {
            ...lastMessage,
            status: status,
            ...autoCollapseState,
          },
        ];

        return updatedMessages;
      });
    },
    [setMessage, applyAutoCollapseLogic],
  );

  // 非流式请求
  const handleNonStreamRequest = useCallback(
    async (payload, request) => {
      setDebugData((prev) => ({
        ...prev,
        request: payload,
        timestamp: new Date().toISOString(),
        response: null,
        sseMessages: null, // 非流式请求清除 SSE 消息
        isStreaming: false,
      }));
      setActiveDebugTab(DEBUG_TABS.REQUEST);

      try {
        const response = await fetch(API_ENDPOINTS.CHAT_COMPLETIONS, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'New-Api-User': session.userId,
          },
          body: JSON.stringify(payload),
          signal: request.controller.signal,
        });
        if (!isActive(request)) return;

        if (!response.ok) {
          let errorBody = '';
          let parsedError = null;
          try {
            errorBody = await response.text();
            const errorJson = JSON.parse(errorBody);
            if (errorJson?.error) {
              parsedError = errorJson.error;
            }
          } catch (e) {
            if (!errorBody) {
              errorBody = '无法读取错误响应体';
            }
          }

          if (!isActive(request)) return;
          const errorInfo = handleApiError(
            new Error(
              `HTTP error! status: ${response.status}, body: ${errorBody}`,
            ),
            response,
          );

          setDebugData((prev) => ({
            ...prev,
            response: JSON.stringify(errorInfo, null, 2),
          }));
          setActiveDebugTab(DEBUG_TABS.RESPONSE);

          const err = new Error(
            parsedError?.message ||
              `HTTP error! status: ${response.status}, body: ${errorBody}`,
          );
          err.errorCode = parsedError?.code || null;
          err.errorType = parsedError?.type || null;
          throw err;
        }

        const data = await response.json();
        if (!isActive(request)) return;
        if (!data.choices?.[0])
          throw new Error(data.error?.message || t('请求发生错误'));

        setDebugData((prev) => ({
          ...prev,
          response: JSON.stringify(data, null, 2),
        }));
        setActiveDebugTab(DEBUG_TABS.RESPONSE);

        if (data.choices?.[0]) {
          const choice = data.choices[0];
          let content = choice.message?.content || '';
          let reasoningContent =
            choice.message?.reasoning_content ||
            choice.message?.reasoning ||
            '';

          const processed = processThinkTags(content, reasoningContent);

          setMessage((prevMessage) => {
            const newMessages = [...prevMessage];
            const lastMessage = newMessages[newMessages.length - 1];
            if (lastMessage?.status === MESSAGE_STATUS.LOADING) {
              const autoCollapseState = applyAutoCollapseLogic(
                lastMessage,
                true,
              );

              newMessages[newMessages.length - 1] = {
                ...lastMessage,
                content: processed.content,
                reasoningContent: processed.reasoningContent,
                status: MESSAGE_STATUS.COMPLETE,
                ...autoCollapseState,
              };
            }
            return newMessages;
          });
        }
      } catch (error) {
        if (!isActive(request)) return;
        console.error('Non-stream request error:', error);

        const errorInfo = handleApiError(error);
        setDebugData((prev) => ({
          ...prev,
          response: JSON.stringify(errorInfo, null, 2),
        }));
        setActiveDebugTab(DEBUG_TABS.RESPONSE);

        setMessage((prevMessage) => {
          const newMessages = [...prevMessage];
          const lastMessage = newMessages[newMessages.length - 1];
          if (lastMessage?.status === MESSAGE_STATUS.LOADING) {
            const autoCollapseState = applyAutoCollapseLogic(lastMessage, true);

            newMessages[newMessages.length - 1] = {
              ...lastMessage,
              content: t('请求发生错误: ') + error.message,
              errorCode: error.errorCode || null,
              status: MESSAGE_STATUS.ERROR,
              ...autoCollapseState,
            };
          }
          return newMessages;
        });
      } finally {
        release(request);
      }
    },
    [
      setDebugData,
      setActiveDebugTab,
      setMessage,
      t,
      applyAutoCollapseLogic,
      session,
      isActive,
      release,
    ],
  );

  // SSE请求
  const handleSSE = useCallback(
    (payload, request) => {
      setDebugData((prev) => ({
        ...prev,
        request: payload,
        timestamp: new Date().toISOString(),
        response: null,
        sseMessages: [], // 新增：存储 SSE 消息数组
        isStreaming: true, // 新增：标记流式状态
      }));
      setActiveDebugTab(DEBUG_TABS.REQUEST);

      const source = new SSE(API_ENDPOINTS.CHAT_COMPLETIONS, {
        start: false,
        headers: {
          'Content-Type': 'application/json',
          'New-Api-User': session.userId,
        },
        method: 'POST',
        payload: JSON.stringify(payload),
      });

      request.source = source;
      sseSourceRef.current = source;

      let responseData = '';
      let hasReceivedFirstResponse = false;
      let isStreamComplete = false; // 添加标志位跟踪流是否正常完成
      const accumulate = createStreamTextAccumulator();
      let pending = { reasoning: '', content: '', events: [] };
      let timer;
      request.cancel = () => {
        clearTimeout(timer);
        timer = undefined;
        pending = { reasoning: '', content: '', events: [] };
      };
      request.flush = () => {
        clearTimeout(timer);
        timer = undefined;
        if (!isActive(request)) return request.cancel();
        const batch = pending;
        pending = { reasoning: '', content: '', events: [] };
        if (batch.reasoning) streamMessageUpdate(batch.reasoning, 'reasoning');
        if (batch.content) streamMessageUpdate(batch.content, 'content');
        if (batch.events.length) {
          setDebugData((prev) => ({
            ...prev,
            sseMessages: [...(prev.sseMessages || []), ...batch.events].slice(-200),
          }));
        }
      };

      source.addEventListener('message', (e) => {
        if (!isActive(request)) return;
        if (e.data === '[DONE]') {
          isStreamComplete = true; // 标记流正常完成
          release(request);
          setDebugData((prev) => ({
            ...prev,
            response: responseData,
            sseMessages: [...(prev.sseMessages || []), '[DONE]'], // 添加 DONE 标记
            isStreaming: false,
          }));
          completeMessage();
          return;
        }

        try {
          const payload = JSON.parse(e.data);
          if (payload.error) {
            const error = new Error(payload.error.message || t('请求发生错误'));
            error.errorCode = payload.error.code;
            error.isApiError = true;
            throw error;
          }
          responseData += e.data + '\n';

          if (!hasReceivedFirstResponse) {
            setActiveDebugTab(DEBUG_TABS.RESPONSE);
            hasReceivedFirstResponse = true;
          }

          // 新增：将 SSE 消息添加到数组
          pending.events.push(e.data);
          const choice = payload.choices?.[0];
          const updates = accumulate(choice?.delta ?? choice?.message ?? choice, !choice?.delta);
          pending.reasoning += updates.reasoning;
          pending.content += updates.content;
          if (!timer) timer = setTimeout(request.flush, 50);
        } catch (error) {
          request.flush();
          console.error('Failed to parse SSE message:', error);
          const errorInfo = `解析错误: ${error.message}`;

          setDebugData((prev) => ({
            ...prev,
            response: responseData + `\n\nError: ${errorInfo}`,
            sseMessages: [...(prev.sseMessages || []), e.data], // 即使解析失败也保存原始数据
            isStreaming: false,
          }));
          setActiveDebugTab(DEBUG_TABS.RESPONSE);

          setMessage((messages) =>
            messages.map((message, index) =>
              index === messages.length - 1
                ? {
                    ...message,
                    content:
                      (message.content || '') +
                      (error.isApiError
                        ? error.message
                        : t('解析响应数据时发生错误')),
                    errorCode: error.errorCode || null,
                    status: MESSAGE_STATUS.ERROR,
                    ...applyAutoCollapseLogic(message, true),
                  }
                : message,
            ),
          );
          release(request);
        }
      });

      source.addEventListener('error', (e) => {
        if (!isActive(request)) return;
        request.flush();
        if (!isStreamComplete) {
          console.error('SSE Error:', e);
          let errorMessage = e.data || t('请求发生错误');
          let errorCode = null;

          if (e.data) {
            try {
              const errorJson = JSON.parse(e.data);
              if (errorJson?.error) {
                errorMessage = errorJson.error.message || errorMessage;
                errorCode = errorJson.error.code || null;
              }
            } catch (_) {
              // not JSON, use raw data as error message
            }
          }

          const errorInfo = handleApiError(new Error(errorMessage));
          errorInfo.readyState = source.readyState;

          setDebugData((prev) => ({
            ...prev,
            response:
              responseData +
              '\n\nSSE Error:\n' +
              JSON.stringify(errorInfo, null, 2),
            isStreaming: false,
          }));
          setActiveDebugTab(DEBUG_TABS.RESPONSE);

          setMessage((prevMessage) => {
            const newMessages = [...prevMessage];
            const lastMessage = newMessages[newMessages.length - 1];
            if (
              lastMessage &&
              lastMessage.status !== MESSAGE_STATUS.COMPLETE &&
              lastMessage.status !== MESSAGE_STATUS.ERROR
            ) {
              newMessages[newMessages.length - 1] = {
                ...lastMessage,
                content: (lastMessage.content || '') + errorMessage,
                errorCode: errorCode,
                status: MESSAGE_STATUS.ERROR,
                ...applyAutoCollapseLogic(lastMessage, true),
              };
            }
            return newMessages;
          });
          release(request);
        }
      });

      source.addEventListener('readystatechange', (e) => {
        if (!isActive(request)) return;
        // 未收到 DONE 即断开也必须结束请求，避免遗留生成锁。
        if (e.readyState >= 2 && !isStreamComplete) {
          request.flush();
          const errorInfo = handleApiError(new Error('HTTP状态错误'));
          errorInfo.status = source.status;
          errorInfo.readyState = source.readyState;

          setDebugData((prev) => ({
            ...prev,
            response:
              responseData +
              '\n\nHTTP Error:\n' +
              JSON.stringify(errorInfo, null, 2),
            isStreaming: false,
          }));
          setActiveDebugTab(DEBUG_TABS.RESPONSE);

          streamMessageUpdate(t('连接已断开'), 'content');
          completeMessage(MESSAGE_STATUS.ERROR);
          release(request);
        }
      });

      try {
        source.stream();
      } catch (error) {
        console.error('Failed to start SSE stream:', error);
        const errorInfo = handleApiError(error);

        setDebugData((prev) => ({
          ...prev,
          response: 'Stream启动失败:\n' + JSON.stringify(errorInfo, null, 2),
          isStreaming: false,
        }));
        setActiveDebugTab(DEBUG_TABS.RESPONSE);

        if (!isActive(request)) return;
        streamMessageUpdate(t('建立连接时发生错误'), 'content');
        completeMessage(MESSAGE_STATUS.ERROR);
        release(request);
      }
    },
    [
      setDebugData,
      setActiveDebugTab,
      setMessage,
      streamMessageUpdate,
      completeMessage,
      t,
      applyAutoCollapseLogic,
      session,
      isActive,
      release,
      sseSourceRef,
    ],
  );

  // 停止生成
  const onStopGenerator = useCallback(() => {
    const request = activeRequestRef.current;
    if (!request || !isActive(request)) return;
    release(request);

    // 无论是否存在 SSE 连接，都尝试处理最后一条正在生成的消息
    setMessage((prevMessage) => {
      if (prevMessage.length === 0) return prevMessage;
      const lastMessage = prevMessage[prevMessage.length - 1];

      if (
        lastMessage.status === MESSAGE_STATUS.LOADING ||
        lastMessage.status === MESSAGE_STATUS.INCOMPLETE
      ) {
        const processed = processIncompleteThinkTags(
          lastMessage.content || '',
          lastMessage.reasoningContent || '',
        );

        const autoCollapseState = applyAutoCollapseLogic(lastMessage, true);

        const updatedMessages = [
          ...prevMessage.slice(0, -1),
          {
            ...lastMessage,
            status: MESSAGE_STATUS.COMPLETE,
            reasoningContent: processed.reasoningContent || null,
            content: processed.content,
            ...autoCollapseState,
          },
        ];

        return updatedMessages;
      }
      return prevMessage;
    });
    setDebugData((prev) => ({ ...prev, isStreaming: false }));
  }, [setMessage, setDebugData, applyAutoCollapseLogic, isActive, release]);

  // 发送请求
  const sendRequest = useCallback(
    (payload, isStream, messages) => {
      if (!canSend()) return false;
      const request = { controller: new AbortController(), source: null };
      activeRequestRef.current = request;
      try {
        if (messages) setMessage(messages);
        if (isStream) {
          handleSSE(payload, request);
        } else {
          handleNonStreamRequest(payload, request);
        }
      } catch (error) {
        if (isActive(request)) {
          setDebugData((prev) => ({ ...prev, isStreaming: false }));
          streamMessageUpdate(t('建立连接时发生错误'), 'content');
          completeMessage(MESSAGE_STATUS.ERROR);
        }
        release(request);
      }
      return true;
    },
    [
      canSend,
      setMessage,
      setDebugData,
      handleSSE,
      handleNonStreamRequest,
      isActive,
      streamMessageUpdate,
      completeMessage,
      release,
      t,
    ],
  );

  return {
    sendRequest,
    canSend,
    onStopGenerator,
    streamMessageUpdate,
    completeMessage,
  };
};

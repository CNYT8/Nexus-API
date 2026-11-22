/*
Copyright (C) 2023-2026 QuantumNous

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
import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { sendChatCompletion } from '../api'
import { MESSAGE_STATUS, ERROR_MESSAGES } from '../constants'
import {
  buildChatCompletionPayload,
  updateAssistantMessageWithError,
  updateLastAssistantMessage,
  processStreamingContent,
  finalizeMessage,
} from '../lib'
import type { Message, PlaygroundConfig, ParameterEnabled } from '../types'
import type { AccountSession } from './use-account-session'
import { useStreamRequest } from './use-stream-request'

interface UseChatHandlerOptions {
  config: PlaygroundConfig
  parameterEnabled: ParameterEnabled
  onMessageUpdate: (
    updater: Message[] | ((prev: Message[]) => Message[])
  ) => void
  session: AccountSession
}

interface ChatRequest {
  controller: AbortController
}

/**
 * Hook for handling chat message sending and receiving
 */
export function useChatHandler({
  config,
  parameterEnabled,
  onMessageUpdate,
  session,
}: UseChatHandlerOptions) {
  const { sendStreamRequest, stopStream, flushStream } = useStreamRequest(session.userId)
  const activeRequestRef = useRef<ChatRequest | null>(null)
  const mountedRef = useRef(true)
  const [isGenerating, setIsGenerating] = useState(false)
  const isActive = useCallback(
    (request: ChatRequest) =>
      mountedRef.current &&
      activeRequestRef.current === request &&
      session.isCurrent(),
    [session]
  )
  const canSend = useCallback(
    () =>
      mountedRef.current && session.isCurrent() && !activeRequestRef.current,
    [session]
  )
  const release = useCallback(
    (request: ChatRequest) => {
      if (activeRequestRef.current !== request) return
      activeRequestRef.current = null
      request.controller.abort()
      stopStream()
      if (mountedRef.current) setIsGenerating(false)
    },
    [stopStream]
  )
  useEffect(() => {
    mountedRef.current = true
    const cancel = () => {
      const request = activeRequestRef.current
      if (request) release(request)
    }
    const unsubscribe = session.onInvalidate(cancel)
    return () => {
      mountedRef.current = false
      unsubscribe()
      cancel()
    }
  }, [session, release])

  // Handle stream update
  const handleStreamUpdate = useCallback(
    (type: 'reasoning' | 'content', chunk: string) => {
      onMessageUpdate((prev) =>
        updateLastAssistantMessage(prev, (message) => {
          if (message.status === MESSAGE_STATUS.ERROR) return message

          if (type === 'reasoning') {
            // Direct API reasoning_content
            return {
              ...message,
              reasoning: {
                content: (message.reasoning?.content || '') + chunk,
                duration: 0,
              },
              isReasoningStreaming: true,
              status: MESSAGE_STATUS.STREAMING,
            }
          }

          // Content streaming: handle <think> tags
          return {
            ...processStreamingContent(message, chunk),
            status: MESSAGE_STATUS.STREAMING,
          }
        })
      )
    },
    [onMessageUpdate]
  )

  // Handle stream complete
  const handleStreamComplete = useCallback(() => {
    onMessageUpdate((prev) =>
      updateLastAssistantMessage(prev, (message) =>
        message.status === MESSAGE_STATUS.COMPLETE ||
        message.status === MESSAGE_STATUS.ERROR
          ? message
          : { ...finalizeMessage(message), status: MESSAGE_STATUS.COMPLETE }
      )
    )
  }, [onMessageUpdate])

  // Handle stream error
  const handleStreamError = useCallback(
    (error: string, errorCode?: string) => {
      toast.error(error)
      onMessageUpdate((prev) =>
        updateAssistantMessageWithError(prev, error, errorCode)
      )
    },
    [onMessageUpdate]
  )

  // Send streaming chat request
  const sendStreamingChat = useCallback(
    (messages: Message[], request: ChatRequest) => {
      const payload = buildChatCompletionPayload(
        messages,
        config,
        parameterEnabled
      )
      const started = sendStreamRequest(
        payload,
        (type, chunk) => {
          if (isActive(request)) handleStreamUpdate(type, chunk)
        },
        () => {
          if (!isActive(request)) return
          handleStreamComplete()
          release(request)
        },
        (error, code) => {
          if (!isActive(request)) return
          handleStreamError(error, code)
          release(request)
        }
      )
      if (!started) throw new Error(ERROR_MESSAGES.STREAM_START_ERROR)
    },
    [
      config,
      parameterEnabled,
      sendStreamRequest,
      handleStreamUpdate,
      handleStreamComplete,
      handleStreamError,
      isActive,
      release,
    ]
  )

  // Send non-streaming chat request
  const sendNonStreamingChat = useCallback(
    async (messages: Message[], request: ChatRequest) => {
      try {
        const payload = buildChatCompletionPayload(
          messages,
          config,
          parameterEnabled
        )
        const response = await sendChatCompletion(
          payload,
          session.userId,
          request.controller.signal
        )
        if (!isActive(request)) return
        const choice = response.choices?.[0]
        if (!choice) throw new Error(ERROR_MESSAGES.API_REQUEST_ERROR)

        onMessageUpdate((prev) =>
          updateLastAssistantMessage(prev, (message) => ({
            ...finalizeMessage(
              {
                ...message,
                versions: [
                  {
                    ...message.versions[0],
                    content: choice.message?.content || '',
                  },
                  ...message.versions.slice(1),
                ],
              },
              choice.message?.reasoning_content || choice.message?.reasoning
            ),
            status: MESSAGE_STATUS.COMPLETE,
          }))
        )
      } catch (error: unknown) {
        if (!isActive(request)) return
        const err = error as {
          response?: {
            data?: { message?: string; error?: { code?: string } }
          }
          message?: string
        }
        handleStreamError(
          err?.response?.data?.message ||
            err?.message ||
            ERROR_MESSAGES.API_REQUEST_ERROR,
          err?.response?.data?.error?.code || undefined
        )
      } finally {
        release(request)
      }
    },
    [
      config,
      parameterEnabled,
      onMessageUpdate,
      handleStreamError,
      session,
      isActive,
      release,
    ]
  )

  // Send chat request (stream or non-stream based on config)
  const sendChat = useCallback(
    (messages: Message[]) => {
      if (!canSend()) return false
      const request = { controller: new AbortController() }
      activeRequestRef.current = request
      setIsGenerating(true)
      try {
        onMessageUpdate(messages)
        if (config.stream) {
          sendStreamingChat(messages, request)
        } else {
          void sendNonStreamingChat(messages, request)
        }
      } catch {
        if (isActive(request))
          handleStreamError(ERROR_MESSAGES.STREAM_START_ERROR)
        release(request)
      }
      return true
    },
    [
      canSend,
      config.stream,
      onMessageUpdate,
      sendStreamingChat,
      sendNonStreamingChat,
      isActive,
      handleStreamError,
      release,
    ]
  )

  // Stop generation
  const stopGeneration = useCallback(() => {
    const request = activeRequestRef.current
    if (!request || !isActive(request)) return
    flushStream()
    release(request)
    onMessageUpdate((prev) =>
      updateLastAssistantMessage(prev, (message) =>
        message.status === MESSAGE_STATUS.LOADING ||
        message.status === MESSAGE_STATUS.STREAMING
          ? { ...finalizeMessage(message), status: MESSAGE_STATUS.COMPLETE }
          : message
      )
    )
  }, [release, isActive, onMessageUpdate, flushStream])

  return {
    sendChat,
    stopGeneration,
    isGenerating,
    canSend,
  }
}

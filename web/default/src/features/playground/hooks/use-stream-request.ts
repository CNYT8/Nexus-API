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
import { useCallback, useEffect, useRef } from 'react'
import { SSE } from 'sse.js'
import { API_ENDPOINTS, ERROR_MESSAGES } from '../constants'
import type { ChatCompletionRequest, ChatCompletionChunk } from '../types'
import { createStreamTextAccumulator } from '../lib/stream-text'

/**
 * Hook for handling streaming chat completion requests
 */
export function useStreamRequest(userId: string | null) {
  const sseSourceRef = useRef<SSE | null>(null)
  const pendingRef = useRef<{ flush: () => void; cancel: () => void } | null>(null)

  const sendStreamRequest = useCallback(
    (
      payload: ChatCompletionRequest,
      onUpdate: (type: 'reasoning' | 'content', chunk: string) => void,
      onComplete: () => void,
      onError: (error: string, errorCode?: string) => void
    ) => {
      if (sseSourceRef.current || !userId) return false
      const source = new SSE(API_ENDPOINTS.CHAT_COMPLETIONS, {
        start: false,
        headers: { 'Content-Type': 'application/json', 'New-Api-User': userId },
        method: 'POST',
        payload: JSON.stringify(payload),
      })

      sseSourceRef.current = source
      let settled = false
      const isActive = () => !settled && sseSourceRef.current === source
      const accumulate = createStreamTextAccumulator()
      let pending = { reasoning: '', content: '' }
      let timer: ReturnType<typeof setTimeout> | undefined
      const cancel = () => {
        clearTimeout(timer)
        timer = undefined
        pending = { reasoning: '', content: '' }
      }
      const flush = () => {
        clearTimeout(timer)
        timer = undefined
        if (!isActive()) return cancel()
        const batch = pending
        pending = { reasoning: '', content: '' }
        if (batch.reasoning) onUpdate('reasoning', batch.reasoning)
        if (batch.content) onUpdate('content', batch.content)
      }
      pendingRef.current = { flush, cancel }
      const closeSource = () => {
        cancel()
        pendingRef.current = null
        settled = true
        if (sseSourceRef.current === source) sseSourceRef.current = null
        source.close()
      }

      const handleError = (errorMessage: string, errorCode?: string) => {
        if (!isActive()) return
        flush()
        closeSource()
        onError(errorMessage, errorCode)
      }

      source.addEventListener('message', (e: MessageEvent) => {
        if (!isActive()) return
        if (e.data === '[DONE]') {
          flush()
          closeSource()
          onComplete()
          return
        }

        try {
          const chunk: ChatCompletionChunk & {
            error?: { message?: string; code?: string }
          } = JSON.parse(e.data)
          if (chunk.error) {
            handleError(
              chunk.error.message || ERROR_MESSAGES.API_REQUEST_ERROR,
              chunk.error.code
            )
            return
          }
          const choice = chunk.choices?.[0]
          const updates = accumulate(choice?.delta ?? choice?.message ?? choice, !choice?.delta)
          pending.reasoning += updates.reasoning
          pending.content += updates.content
          if (!timer && (pending.reasoning || pending.content)) {
            timer = setTimeout(flush, 50)
          }
        } catch (error) {
          // eslint-disable-next-line no-console
          console.error('Failed to parse SSE message:', error)
          handleError(ERROR_MESSAGES.PARSE_ERROR)
        }
      })

      source.addEventListener('error', (e: Event & { data?: string }) => {
        if (isActive()) {
          // eslint-disable-next-line no-console
          console.error('SSE Error:', e)
          let errorMessage = e.data || ERROR_MESSAGES.API_REQUEST_ERROR
          let errorCode: string | undefined
          if (e.data) {
            try {
              const parsed = JSON.parse(e.data) as {
                error?: { message?: string; code?: string }
              }
              if (parsed?.error) {
                errorMessage = parsed.error.message || errorMessage
                errorCode = parsed.error.code || undefined
              }
            } catch {
              // not JSON, use raw string
            }
          }
          handleError(errorMessage, errorCode)
        }
      })

      source.addEventListener(
        'readystatechange',
        (e: Event & { readyState?: number }) => {
          const status = (source as unknown as { status?: number }).status
          if (e.readyState !== undefined && e.readyState >= 2) {
            handleError(
              status && status >= 400
                ? `HTTP ${status}: ${ERROR_MESSAGES.CONNECTION_CLOSED}`
                : ERROR_MESSAGES.CONNECTION_CLOSED
            )
          }
        }
      )

      try {
        source.stream()
      } catch (error: unknown) {
        // eslint-disable-next-line no-console
        console.error('Failed to start SSE stream:', error)
        handleError(ERROR_MESSAGES.STREAM_START_ERROR)
      }
      return true
    },
    [userId]
  )

  const flushStream = useCallback(() => pendingRef.current?.flush(), [])
  const stopStream = useCallback(() => {
    pendingRef.current?.cancel()
    pendingRef.current = null
    const source = sseSourceRef.current
    sseSourceRef.current = null
    source?.close()
  }, [])

  useEffect(() => stopStream, [stopStream])

  return { sendStreamRequest, stopStream, flushStream }
}

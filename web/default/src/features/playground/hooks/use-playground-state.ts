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
import { useState, useCallback, useRef, useEffect } from 'react'
import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../constants'
import {
  loadConfig,
  saveConfig,
  loadParameterEnabled,
  saveParameterEnabled,
  loadMessages,
  saveMessages,
  sanitizeMessagesOnLoad,
} from '../lib'
import type {
  Message,
  PlaygroundConfig,
  ParameterEnabled,
  ModelOption,
  GroupOption,
} from '../types'
import type { AccountSession } from './use-account-session'

/**
 * Main state management hook for playground
 */
export function usePlaygroundState(session: AccountSession) {
  // Load initial state from localStorage
  const [config, setConfig] = useState<PlaygroundConfig>(() => {
    const savedConfig = loadConfig(session.userId)
    return { ...DEFAULT_CONFIG, ...savedConfig }
  })

  const [parameterEnabled, setParameterEnabled] = useState<ParameterEnabled>(
    () => {
      const saved = loadParameterEnabled(session.userId)
      return { ...DEFAULT_PARAMETER_ENABLED, ...saved }
    }
  )

  const [messages, setMessages] = useState<Message[]>(() => {
    return loadMessages(session.userId) || []
  })

  const messagesRef = useRef(messages)
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const messagesDirtyRef = useRef(false)
  const configRef = useRef(config)
  const parameterRef = useRef(parameterEnabled)
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    let finalized = false
    const finalize = () => {
      if (finalized) return
      finalized = true
      // 只归档一次，避免旧页面随后卸载时覆盖同账号新会话的历史。
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = undefined
      const saved = sanitizeMessagesOnLoad(messagesRef.current)
      if (messagesDirtyRef.current || saved !== messagesRef.current) {
        messagesRef.current = saved
        messagesDirtyRef.current = !saveMessages(saved, session.userId)
      }
    }
    const persist = () => {
      if (!session.isCurrent() || finalized || !messagesDirtyRef.current) return
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = undefined
      messagesDirtyRef.current = !saveMessages(messagesRef.current, session.userId)
    }
    window.addEventListener('pagehide', persist)
    window.addEventListener('beforeunload', persist)
    const unsubscribe = session.onInvalidate(finalize)
    return () => {
      window.removeEventListener('pagehide', persist)
      window.removeEventListener('beforeunload', persist)
      mountedRef.current = false
      unsubscribe()
      finalize()
    }
  }, [session])
  const isCurrent = useCallback(
    () => mountedRef.current && session.isCurrent(),
    [session]
  )
  const getMessages = useCallback(() => messagesRef.current, [])

  const [models, setModels] = useState<ModelOption[]>([])
  const [groups, setGroups] = useState<GroupOption[]>([])

  // Update config with automatic save
  const updateConfig = useCallback(
    <K extends keyof PlaygroundConfig>(key: K, value: PlaygroundConfig[K]) => {
      if (!isCurrent()) return
      const updated = { ...configRef.current, [key]: value }
      configRef.current = updated
      setConfig(updated)
      saveConfig(updated, session.userId)
    },
    [isCurrent, session]
  )

  // Update parameter enabled with automatic save
  const updateParameterEnabled = useCallback(
    (key: keyof ParameterEnabled, value: boolean) => {
      if (!isCurrent()) return
      const updated = { ...parameterRef.current, [key]: value }
      parameterRef.current = updated
      setParameterEnabled(updated)
      saveParameterEnabled(updated, session.userId)
    },
    [isCurrent, session]
  )

  // Update messages with automatic save
  const updateMessages = useCallback(
    (updater: Message[] | ((prev: Message[]) => Message[])) => {
      if (!isCurrent()) return
      const next =
        typeof updater === 'function' ? updater(messagesRef.current) : updater
      if (next === messagesRef.current) return
      messagesRef.current = next
      setMessages(next)
      messagesDirtyRef.current = true
      const streaming = next.some((m) => m.status === 'loading' || m.status === 'streaming')
      if (!streaming) {
        clearTimeout(saveTimerRef.current)
        saveTimerRef.current = undefined
        messagesDirtyRef.current = !saveMessages(next, session.userId)
      } else if (!saveTimerRef.current) {
        saveTimerRef.current = setTimeout(() => {
          saveTimerRef.current = undefined
          if (!isCurrent()) return
          messagesDirtyRef.current = !saveMessages(messagesRef.current, session.userId)
        }, 500)
      }
    },
    [isCurrent, session]
  )

  // Clear all messages
  const clearMessages = useCallback(() => {
    updateMessages([])
  }, [updateMessages])

  // Reset config to defaults
  const resetConfig = useCallback(() => {
    if (!isCurrent()) return
    configRef.current = DEFAULT_CONFIG
    parameterRef.current = DEFAULT_PARAMETER_ENABLED
    setConfig(DEFAULT_CONFIG)
    setParameterEnabled(DEFAULT_PARAMETER_ENABLED)
    saveConfig(DEFAULT_CONFIG, session.userId)
    saveParameterEnabled(DEFAULT_PARAMETER_ENABLED, session.userId)
  }, [isCurrent, session])

  return {
    // State
    config,
    parameterEnabled,
    messages,
    models,
    groups,

    // Setters
    setModels,
    setGroups,

    // Actions
    updateConfig,
    updateParameterEnabled,
    updateMessages,
    getMessages,
    clearMessages,
    resetConfig,
  }
}

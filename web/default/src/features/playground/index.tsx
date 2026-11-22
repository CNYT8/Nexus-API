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
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { getUserModels, getUserGroups } from './api'
import { PlaygroundChat } from './components/playground-chat'
import { PlaygroundInput } from './components/playground-input'
import { usePlaygroundState, useChatHandler } from './hooks'
import { useEditGuard } from './hooks/use-edit-guard'
import {
  useAccountSession,
  type AccountSession,
} from './hooks/use-account-session'
import { createUserMessage, createLoadingAssistantMessage } from './lib'
import type { Message as MessageType } from './types'

export function Playground() {
  const session = useAccountSession()
  return session.userId ? (
    <PlaygroundSession key={session.revision} session={session} />
  ) : null
}

function PlaygroundSession({ session }: { session: AccountSession }) {
  const { t } = useTranslation()
  const {
    config,
    parameterEnabled,
    messages,
    models,
    groups,
    updateMessages,
    getMessages,
    setModels,
    setGroups,
    updateConfig,
    updateParameterEnabled,
  } = usePlaygroundState(session)

  const { sendChat, stopGeneration, isGenerating, canSend } = useChatHandler({
    session,
    config,
    parameterEnabled,
    onMessageUpdate: updateMessages,
  })

  // Edit dialog state
  const [editingMessageKey, setEditingMessageKey] = useState<string | null>(
    null
  )

  const [editDirty, setEditDirty] = useState(false)
  const { confirmDiscard, dialogProps } = useEditGuard(editDirty, session)
  const resetEdit = useCallback(() => {
    setEditingMessageKey(null)
    setEditDirty(false)
  }, [])

  const { userId, revision } = session

  // 查询身份只依赖账号与会话代次；提示和结果回填另行检查当前会话。
  const {
    data: modelsData,
    isLoading: isLoadingModels,
    error: modelsError,
  } = useQuery({
    queryKey: ['playground-models', userId, revision, config.group],
    queryFn: ({ signal }) => getUserModels(userId, config.group, signal),
    retry: false,
  })

  const { data: groupsData, error: groupsError } = useQuery({
    queryKey: ['playground-groups', userId, revision],
    queryFn: ({ signal }) => getUserGroups(userId, signal),
    retry: false,
  })

  useEffect(() => {
    if (!session.isCurrent()) return
    if (modelsError) {
      toast.error(
        modelsError instanceof Error
          ? modelsError.message
          : t('Failed to load playground models')
      )
    }
    if (groupsError) {
      toast.error(
        groupsError instanceof Error
          ? groupsError.message
          : t('Failed to load playground groups')
      )
    }
  }, [modelsError, groupsError, session, t])

  // Update models when data changes
  useEffect(() => {
    if (!session.isCurrent()) return
    setModels(modelsData ?? [])
    if (!modelsData) return

    // Set default model if current model is not available
    const isCurrentModelValid = modelsData.some((m) => m.value === config.model)
    if (!isCurrentModelValid) {
      updateConfig('model', modelsData[0]?.value ?? '')
    }
  }, [modelsData, config.model, setModels, updateConfig, session])

  // Update groups when data changes
  useEffect(() => {
    if (!groupsData || !session.isCurrent()) return

    setGroups(groupsData)

    const hasCurrentGroup = groupsData.some((g) => g.value === config.group)
    if (!hasCurrentGroup && groupsData.length > 0) {
      const fallback =
        groupsData.find((g) => g.value === 'default')?.value ??
        groupsData[0].value
      updateConfig('group', fallback)
    }
  }, [groupsData, setGroups, config.group, updateConfig, session])

  // Re-read the active model after a confirmation; a group/query may have
  // changed while the dialog was open. Every send path uses this gate.
  const submitCurrent = useCallback((nextMessages: MessageType[]) => {
    if (!canSend() || modelsError || !modelsData?.some((m) => m.value === config.model))
      return false
    return sendChat(nextMessages)
  }, [canSend, modelsError, modelsData, config.model, sendChat])
  const submitRef = useRef(submitCurrent)
  useLayoutEffect(() => { submitRef.current = submitCurrent }, [submitCurrent])

  const requestAction = useCallback(async (run: () => boolean, clear = false) => {
    if (!(await confirmDiscard(clear)) || !session.isCurrent()) return false
    if (!run()) return false
    resetEdit()
    return true
  }, [confirmDiscard, session, resetEdit])

  const handleSendMessage = (text: string, images?: string[]) => requestAction(() => {
    const userMessage = createUserMessage(text, images)
    const assistantMessage = createLoadingAssistantMessage()
    return submitRef.current([...getMessages(), userMessage, assistantMessage])
  })

  const handleCopyMessage = (message: MessageType) => {
    // Copy is handled in MessageActions component
    // eslint-disable-next-line no-console
    console.log('Message copied:', message.key)
  }

  const handleRegenerateMessage = (message: MessageType) => {
    if (!canSend()) return
    void requestAction(() => {
      const current = getMessages()
      const messageIndex = current.findIndex((m) => m.key === message.key)
      if (messageIndex === -1) return false
      return submitRef.current([
        ...current.slice(0, messageIndex),
        createLoadingAssistantMessage(),
      ])
    })
  }

  const handleEditMessage = useCallback(
    (message: MessageType) => {
      if (canSend() && message.key !== editingMessageKey)
        void confirmDiscard().then((accepted) => {
          if (!accepted || !canSend() || !getMessages().some((m) => m.key === message.key)) return
          setEditDirty(false)
          setEditingMessageKey(message.key)
        })
    },
    [canSend, editingMessageKey, confirmDiscard, getMessages]
  )

  const handleEditOpenChange = useCallback((open: boolean) => {
    if (!open) void requestAction(() => true)
  }, [requestAction])

  // Apply edit and optionally re-submit from the edited user message
  const applyEdit = useCallback(
    (newContent: string, submit: boolean) => {
      if (!canSend() || !editingMessageKey) return
      const current = getMessages()
      const index = current.findIndex((m) => m.key === editingMessageKey)
      if (index === -1) return

      const updated = current.map((m) =>
        m.key === editingMessageKey
          ? {
              ...m,
              versions: [
                { ...m.versions[0], content: newContent },
                ...m.versions.slice(1),
              ],
            }
          : m
      )

      if (!submit || updated[index].from !== 'user') {
        updateMessages(updated)
        resetEdit()
        return
      }

      const toSubmit = [
        ...updated.slice(0, index + 1),
        createLoadingAssistantMessage(),
      ]
      if (submitRef.current(toSubmit)) resetEdit()
    },
    [editingMessageKey, getMessages, updateMessages, canSend, resetEdit]
  )

  const handleDeleteMessage = (message: MessageType) => {
    if (!canSend()) return
    void requestAction(() => {
      if (!canSend()) return false
      const current = getMessages()
      if (!current.some((m) => m.key === message.key)) return false
      updateMessages(current.filter((m) => m.key !== message.key))
      return true
    })
  }

  return (
    <div className='relative flex size-full flex-col overflow-hidden'>
      <div className='mx-auto flex w-full max-w-4xl justify-end px-4'>
        <Button variant='ghost' size='sm' disabled={!messages.length} onClick={() => void requestAction(() => {
          stopGeneration()
          updateMessages([])
          return true
        }, true)}>
          <Trash2 className='mr-2 size-4' />{t('清空对话')}
        </Button>
      </div>
      <ConfirmDialog {...dialogProps} destructive />
      {/* Full-width scroll container: scrolling works even over side whitespace */}
      <div className='flex flex-1 flex-col overflow-hidden'>
        <PlaygroundChat
          messages={messages}
          onCopyMessage={handleCopyMessage}
          onRegenerateMessage={handleRegenerateMessage}
          onEditMessage={handleEditMessage}
          onDeleteMessage={handleDeleteMessage}
          isGenerating={isGenerating}
          editingKey={editingMessageKey}
          onEditDirtyChange={setEditDirty}
          onCancelEdit={handleEditOpenChange}
          onSaveEdit={(newContent) => applyEdit(newContent, false)}
          onSaveEditAndSubmit={(newContent) => applyEdit(newContent, true)}
        />
      </div>

      {/* Input area: center content and constrain to the same container width */}
      <div className='mx-auto w-full max-w-4xl'>
        <PlaygroundInput
          session={session}
          disabled={isGenerating}
          groups={groups}
          groupValue={config.group}
          isGenerating={isGenerating}
          isModelLoading={isLoadingModels}
          modelValue={config.model}
          models={models}
          onGroupChange={(value) => updateConfig('group', value)}
          onModelChange={(value) => updateConfig('model', value)}
          onStop={stopGeneration}
          onSubmit={handleSendMessage}
          imageEnabled
          config={config}
          onConfigChange={updateConfig}
          onParameterEnabledChange={updateParameterEnabled}
          parameterEnabled={parameterEnabled}
        />
      </div>
    </div>
  )
}

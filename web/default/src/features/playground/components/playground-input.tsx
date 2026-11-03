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
import { useState, useRef } from 'react'
import {
  PaperclipIcon,
  FileIcon,
  ImageIcon,
  ScreenShareIcon,
  CameraIcon,
  GlobeIcon,
  SendIcon,
  SquareIcon,
  XIcon,
  BarChartIcon,
  BoxIcon,
  NotepadTextIcon,
  CodeSquareIcon,
  GraduationCapIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  PromptInput,
  PromptInputButton,
  PromptInputFooter,
  PromptInputTextarea,
  PromptInputTools,
  type PromptInputMessage,
} from '@/components/ai-elements/prompt-input'
import { Suggestion, Suggestions } from '@/components/ai-elements/suggestion'
import { ModelGroupSelector } from '@/components/model-group-selector'
import {
  IMAGE_FILE_ACCEPT,
  MAX_IMAGE_FILE_BYTES,
  MAX_TEXT_FILE_BYTES,
  TEXT_FILE_ACCEPT,
  buildAttachmentTextBlock,
  formatFileSize,
  parseImageAttachment,
  parseTextAttachment,
} from '../lib/attachments'
import type { ModelOption, GroupOption } from '../types'

interface PlaygroundInputProps {
  onSubmit: (text: string, images?: string[]) => void
  onStop?: () => void
  disabled?: boolean
  isGenerating?: boolean
  models: ModelOption[]
  modelValue: string
  onModelChange: (value: string) => void
  isModelLoading?: boolean
  groups: GroupOption[]
  groupValue: string
  onGroupChange: (value: string) => void
  /** Inline images (data URLs) currently attached to the prompt. */
  imageUrls?: string[]
  onImageUrlsChange?: (urls: string[]) => void
  /** Whether the image channel is enabled in playground settings. */
  imageEnabled?: boolean
  showToast?: (message: string, level?: 'error' | 'warning' | 'success') => void
}

const suggestions = [
  { icon: BarChartIcon, text: 'Analyze data', color: '#76d0eb' },
  { icon: BoxIcon, text: 'Surprise me', color: '#76d0eb' },
  { icon: NotepadTextIcon, text: 'Summarize text', color: '#ea8444' },
  { icon: CodeSquareIcon, text: 'Code', color: '#6c71ff' },
  { icon: GraduationCapIcon, text: 'Get advice', color: '#76d0eb' },
  { icon: null, text: 'More' },
]

export function PlaygroundInput({
  onSubmit,
  onStop,
  disabled,
  isGenerating,
  models,
  modelValue,
  onModelChange,
  isModelLoading = false,
  groups,
  groupValue,
  onGroupChange,
  imageUrls = [],
  onImageUrlsChange,
  imageEnabled = false,
}: PlaygroundInputProps) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const [attachments, setAttachments] = useState<
    Array<{ id: string; name: string; size: number; text: string }>
  >([])
  const [parsingAttachment, setParsingAttachment] = useState(false)
  const [pendingImages, setPendingImages] = useState<string[]>(() =>
    imageUrls.filter((url) => url && url.trim() !== '')
  )
  const fileInputRef = useRef<HTMLInputElement>(null)
  const imageInputRef = useRef<HTMLInputElement>(null)

  const isModelSelectDisabled =
    disabled || isModelLoading || models.length === 0
  const isGroupSelectDisabled = disabled || groups.length === 0

  const handleSubmit = (message: PromptInputMessage) => {
    const attachmentText = buildAttachmentTextBlock(attachments)
    const combined = [attachmentText, (message.text ?? '').trim()]
      .filter(Boolean)
      .join('\n\n')
    if (!combined || disabled) return
    onSubmit(combined, pendingImages.length > 0 ? pendingImages : undefined)
    setText('')
    setAttachments([])
    setPendingImages([])
  }

  // Text files are decoded in the browser and inlined into the prompt.
  // Nothing is uploaded, so the server never stores user files.
  const handleTextFilesSelected = async (
    event: React.ChangeEvent<HTMLInputElement>
  ) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ''
    if (files.length === 0) return

    setParsingAttachment(true)
    try {
      for (const file of files) {
        if (file.size > MAX_TEXT_FILE_BYTES) {
          toast.warning(
            `${t('File is too large and was skipped')}: ${file.name} (${t('Limit')} ${formatFileSize(MAX_TEXT_FILE_BYTES)})`
          )
          continue
        }

        const result = await parseTextAttachment(file)
        if (!result.ok) {
          toast.warning(
            result.reason === 'too_large'
              ? `${t('File is too large and was skipped')}: ${result.name}`
              : `${t('Only text files are supported (.md, .txt, etc.)')}: ${result.name}`
          )
          continue
        }

        setAttachments((prev) => [
          ...prev,
          {
            id: `${Date.now()}-${result.name}`,
            name: result.name,
            size: result.size,
            text: result.text,
          },
        ])
      }
    } finally {
      setParsingAttachment(false)
    }
  }

  // Images are converted to data URLs and sent via the existing image_url
  // content part; the relay already supports `data:image/...;base64,`.
  const handleImageFilesSelected = async (
    event: React.ChangeEvent<HTMLInputElement>
  ) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ''
    if (files.length === 0) return

    if (!imageEnabled) {
      toast.warning(t('Please enable the image feature in settings first'))
      return
    }

    setParsingAttachment(true)
    try {
      const nextUrls = [...pendingImages]
      for (const file of files) {
        if (file.size > MAX_IMAGE_FILE_BYTES) {
          toast.warning(
            `${t('Image is too large and was skipped')}: ${file.name} (${t('Limit')} ${formatFileSize(MAX_IMAGE_FILE_BYTES)})`
          )
          continue
        }

        const result = await parseImageAttachment(file)
        if (!result.ok) {
          toast.warning(
            result.reason === 'too_large'
              ? `${t('Image is too large and was skipped')}: ${result.name}`
              : `${t('Only image files are supported (PNG, JPEG, WebP, GIF, BMP)')}: ${result.name}`
          )
          continue
        }

        nextUrls.push(result.dataUrl)
      }
      if (nextUrls.length > pendingImages.length) {
        setPendingImages(nextUrls)
        onImageUrlsChange?.(nextUrls)
      }
    } finally {
      setParsingAttachment(false)
    }
  }

  const handleSuggestionClick = (suggestion: string) => {
    onSubmit(suggestion)
  }

  return (
    <div className='grid shrink-0 gap-4 px-1 md:pb-4'>
      <PromptInput groupClassName='rounded-xl' onSubmit={handleSubmit}>
        <input
          ref={fileInputRef}
          type='file'
          multiple
          accept={TEXT_FILE_ACCEPT}
          className='hidden'
          onChange={handleTextFilesSelected}
        />
        <input
          ref={imageInputRef}
          type='file'
          multiple
          accept={IMAGE_FILE_ACCEPT}
          className='hidden'
          onChange={handleImageFilesSelected}
        />
        {attachments.length > 0 && (
          <div className='flex flex-wrap gap-2 px-4 pt-3'>
            {attachments.map((item) => (
              <div
                key={item.id}
                className='bg-muted flex max-w-full items-center gap-1.5 rounded-md px-2 py-1 text-xs'
                title={item.name}
              >
                <FileIcon className='size-3 shrink-0 opacity-70' />
                <span className='max-w-[14rem] truncate'>{item.name}</span>
                <span className='text-muted-foreground'>
                  {formatFileSize(item.size)}
                </span>
                <button
                  type='button'
                  onClick={() =>
                    setAttachments((prev) =>
                      prev.filter((entry) => entry.id !== item.id)
                    )
                  }
                  disabled={disabled}
                  className='hover:text-foreground text-muted-foreground ml-0.5 shrink-0 rounded p-0.5'
                  aria-label={t('Remove attachment')}
                >
                  <XIcon className='size-3' />
                </button>
              </div>
            ))}
          </div>
        )}
        <PromptInputTextarea
          autoComplete='off'
          autoCorrect='off'
          autoCapitalize='off'
          spellCheck={false}
          className='px-5 md:text-base'
          disabled={disabled}
          onChange={(event) => setText(event.target.value)}
          placeholder={t('Ask anything')}
          value={text}
        />

        <PromptInputFooter className='p-2.5'>
          <PromptInputTools>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <PromptInputButton
                    className='border font-medium'
                    disabled={disabled}
                    variant='outline'
                  />
                }
              >
                <PaperclipIcon size={16} />
                <span className='hidden sm:inline'>
                  {parsingAttachment ? t('Parsing...') : t('Attach')}
                </span>
                <span className='sr-only sm:hidden'>{t('Attach')}</span>
              </DropdownMenuTrigger>
              <DropdownMenuContent align='start'>
                <DropdownMenuItem onClick={() => fileInputRef.current?.click()}>
                  <FileIcon className='mr-2' size={16} />
                  {t('Upload file')}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={() => imageInputRef.current?.click()}
                >
                  <ImageIcon className='mr-2' size={16} />
                  {t('Upload photo')}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={() =>
                    toast.info(t('Feature in development'), {
                      description: 'take-screenshot',
                    })
                  }
                >
                  <ScreenShareIcon className='mr-2' size={16} />
                  {t('Take screenshot')}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={() =>
                    toast.info(t('Feature in development'), {
                      description: 'take-photo',
                    })
                  }
                >
                  <CameraIcon className='mr-2' size={16} />
                  {t('Take photo')}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>

            <PromptInputButton
              className='border font-medium'
              disabled={disabled}
              onClick={() => toast.info(t('Search feature in development'))}
              variant='outline'
            >
              <GlobeIcon size={16} />
              <span className='hidden sm:inline'>{t('Search')}</span>
              <span className='sr-only sm:hidden'>{t('Search')}</span>
            </PromptInputButton>
          </PromptInputTools>

          <div className='flex flex-wrap items-center gap-1.5 md:gap-2'>
            <ModelGroupSelector
              selectedModel={modelValue}
              models={models}
              onModelChange={onModelChange}
              selectedGroup={groupValue}
              groups={groups}
              onGroupChange={onGroupChange}
              disabled={isModelSelectDisabled || isGroupSelectDisabled}
            />

            {isGenerating && onStop ? (
              <PromptInputButton
                className='text-foreground font-medium'
                onClick={onStop}
                variant='secondary'
              >
                <SquareIcon className='fill-current' size={16} />
                <span className='hidden sm:inline'>{t('Stop')}</span>
                <span className='sr-only sm:hidden'>{t('Stop')}</span>
              </PromptInputButton>
            ) : (
              <PromptInputButton
                className='text-foreground font-medium'
                disabled={disabled || !text.trim()}
                type='submit'
                variant='secondary'
              >
                <SendIcon size={16} />
                <span className='hidden sm:inline'>{t('Send')}</span>
                <span className='sr-only sm:hidden'>{t('Send')}</span>
              </PromptInputButton>
            )}
          </div>
        </PromptInputFooter>
      </PromptInput>

      <Suggestions>
        {suggestions.map(({ icon: Icon, text, color }) => (
          <Suggestion
            className={`text-xs font-normal sm:text-sm ${
              text === 'More' ? 'hidden sm:flex' : ''
            }`}
            key={text}
            onClick={() => handleSuggestionClick(text)}
            suggestion={text}
          >
            {Icon && <Icon size={16} style={{ color }} />}
            {text}
          </Suggestion>
        ))}
      </Suggestions>
    </div>
  )
}

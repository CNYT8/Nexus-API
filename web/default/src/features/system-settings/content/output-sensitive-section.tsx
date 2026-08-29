import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  getOutputSensitiveConfig,
  updateOutputSensitiveConfig,
} from '../api'
import type { OutputSensitiveConfig } from '../types'
import { SettingsSection } from '../components/settings-section'
import { SettingsPageFormActions } from '../components/settings-page-context'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

const defaultConfig: OutputSensitiveConfig = {
  enabled: false,
  action: 'truncate',
  match_percent: 20,
  patterns: [],
}

function normalizeConfig(
  config: OutputSensitiveConfig | undefined
): OutputSensitiveConfig {
  return {
    enabled: Boolean(config?.enabled),
    action: config?.action === 'error' ? 'error' : 'truncate',
    match_percent: Math.min(
      100,
      Math.max(1, Number(config?.match_percent) || 20)
    ),
    patterns: Array.isArray(config?.patterns) ? [...config.patterns] : [],
  }
}

export function OutputSensitiveSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [config, setConfig] = useState(defaultConfig)
  const [newPattern, setNewPattern] = useState('')

  const configQuery = useQuery({
    queryKey: ['output-sensitive-config'],
    queryFn: getOutputSensitiveConfig,
  })

  useEffect(() => {
    if (configQuery.data?.success && configQuery.data.data) {
      setConfig(normalizeConfig(configQuery.data.data))
    }
  }, [configQuery.data])

  const saveMutation = useMutation({
    mutationFn: () => updateOutputSensitiveConfig(normalizeConfig(config)),
    onSuccess: async (result) => {
      if (!result.success) {
        toast.error(result.message || t('Failed to save settings'))
        return
      }
      await queryClient.invalidateQueries({
        queryKey: ['output-sensitive-config'],
      })
      toast.success(t('Setting updated successfully'))
    },
  })

  const addPattern = () => {
    const value = newPattern.trim()
    if (!value) return
    setConfig((current) => ({
      ...current,
      patterns: current.patterns.includes(value)
        ? current.patterns
        : [...current.patterns, value],
    }))
    setNewPattern('')
  }

  const removePattern = (index: number) => {
    setConfig((current) => ({
      ...current,
      patterns: current.patterns.filter(
        (_, patternIndex) => patternIndex !== index
      ),
    }))
  }

  const busy = configQuery.isLoading || saveMutation.isPending

  return (
    <SettingsSection title={t('Output Sensitive Words')}>
      <SettingsForm
        onSubmit={(event) => {
          event.preventDefault()
          saveMutation.mutate()
        }}
      >
        <SettingsPageFormActions
          onSave={() => saveMutation.mutate()}
          isSaving={busy}
          saveLabel='Save output sensitive settings'
        />

        <SettingsSwitchItem>
          <SettingsSwitchContent>
            <Label>{t('Enable output sensitive filtering')}</Label>
            <p className='text-muted-foreground text-sm'>
              {t(
                'When enabled, Chat, Dashboard playground and API output use this policy.'
              )}
            </p>
          </SettingsSwitchContent>
          <Switch
            checked={config.enabled}
            disabled={busy}
            onCheckedChange={(enabled) =>
              setConfig((current) => ({ ...current, enabled }))
            }
          />
        </SettingsSwitchItem>

        <div className='grid gap-4 md:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='output-sensitive-percent'>
              {t('Match threshold percentage')}
            </Label>
            <Input
              id='output-sensitive-percent'
              type='number'
              min={1}
              max={100}
              value={config.match_percent}
              disabled={busy}
              onChange={(event) =>
                setConfig((current) => ({
                  ...current,
                  match_percent: Number(event.target.value),
                }))
              }
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='output-sensitive-action'>
              {t('Output sensitive action')}
            </Label>
            <select
              id='output-sensitive-action'
              className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
              value={config.action}
              disabled={busy}
              onChange={(event) =>
                setConfig((current) => ({
                  ...current,
                  action: event.target.value === 'error' ? 'error' : 'truncate',
                }))
              }
            >
              <option value='truncate'>{t('Truncate output')}</option>
              <option value='error'>{t('Stop with an error')}</option>
            </select>
          </div>
        </div>

        <div className='space-y-2'>
          <Label htmlFor='output-sensitive-pattern'>
            {t('Add output sensitive pattern')}
          </Label>
          <div className='flex items-end gap-2'>
            <Textarea
              id='output-sensitive-pattern'
              rows={3}
              value={newPattern}
              disabled={busy}
              onChange={(event) => setNewPattern(event.target.value)}
              placeholder={t('Paste a pattern, no newline required')}
              className='min-h-20 font-mono'
            />
            <Button
              type='button'
              variant='outline'
              onClick={addPattern}
              disabled={busy || !newPattern.trim()}
            >
              <Plus data-icon='inline-start' />
              {t('Add')}
            </Button>
          </div>
        </div>

        <div className='space-y-2'>
          <Label>{t('Output sensitive patterns')}</Label>
          <div className='flex min-h-12 flex-wrap items-start gap-2 rounded-md border p-3'>
            {config.patterns.map((pattern, index) => {
              const preview = pattern.replace(/\s+/g, ' ')
              return (
                <Badge
                  key={`${index}-${pattern.length}`}
                  variant='secondary'
                  className='h-auto max-w-full gap-1 py-1 pr-1 whitespace-normal'
                  title={pattern}
                >
                  <span className='max-w-96 truncate'>
                    {preview.length > 80
                      ? `${preview.slice(0, 80)}...`
                      : preview}
                  </span>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-xs'
                    className='size-5'
                    disabled={busy}
                    aria-label={t('Remove')}
                    title={t('Remove')}
                    onClick={() => removePattern(index)}
                  >
                    <X />
                  </Button>
                </Badge>
              )
            })}
          </div>
        </div>
      </SettingsForm>
    </SettingsSection>
  )
}

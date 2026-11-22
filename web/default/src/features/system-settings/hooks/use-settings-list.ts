import type { SetStateAction } from 'react'
import { useForm } from 'react-hook-form'
import i18next from 'i18next'
import { toast } from 'sonner'
import type { ZodType } from 'zod'
import { useResetForm } from './use-reset-form'
import { useUpdateOption } from './use-update-option'

type SettingsListOptions<T extends { id: number }> = {
  enabled: boolean
  data: string
  dataKey: string
  enabledKey: string
  schema: ZodType<T>
}

export function parseSettingsList<T extends { id: number }>(
  raw: string,
  schema: ZodType<T>
): { items: T[]; error: string | null } {
  try {
    const parsed: unknown = JSON.parse(raw || '[]')
    if (!Array.isArray(parsed)) throw new Error('Expected an array')
    const items: T[] = []
    const ids = new Set<number>()
    for (const [index, item] of parsed.entries()) {
      if (!item || typeof item !== 'object' || Array.isArray(item)) {
        throw new Error(`Row ${index + 1}: expected an object`)
      }
      const result = schema.safeParse({
        ...item,
        id: 'id' in item ? item.id : index + 1,
      })
      if (!result.success) {
        throw new Error(`Row ${index + 1}: ${result.error.message}`)
      }
      if (ids.has(result.data.id)) {
        throw new Error(`Row ${index + 1}: duplicate ID ${result.data.id}`)
      }
      ids.add(result.data.id)
      // Preserve additional server fields when the editor changes known fields.
      items.push({ ...item, ...result.data })
    }
    return { items, error: null }
  } catch (error) {
    return { items: [], error: error instanceof Error ? error.message : String(error) }
  }
}

export function useSettingsList<T extends { id: number }>({
  enabled,
  data,
  dataKey,
  enabledKey,
  schema,
}: SettingsListOptions<T>) {
  const updateOption = useUpdateOption()
  const server = parseSettingsList(data, schema)
  // Invalid source data remains intact; never turn it into an empty saved list.
  const defaults = {
    json: server.error ? data : JSON.stringify(server.items),
    enabled,
  }
  const form = useForm({ defaultValues: defaults })
  const submitForm = useResetForm(form, defaults)
  const draft = parseSettingsList(form.watch('json'), schema)
  const parseError = server.error || draft.error
  const reportParseError = () => {
    toast.error(i18next.t('Invalid settings list. Fix the source data and reload before editing.'))
  }
  const setItems = (next: SetStateAction<T[]>) => {
    const current = parseSettingsList(form.getValues('json'), schema)
    if (server.error || current.error) {
      reportParseError()
      return
    }
    const value = typeof next === 'function' ? next(current.items) : next
    form.setValue('json', JSON.stringify(value), { shouldDirty: true })
  }
  const save = submitForm(async (values) => {
    if (server.error || parseSettingsList(values.json, schema).error) {
      reportParseError()
      return
    }
    await updateOption.saveOptions([
      { key: dataKey, value: values.json },
      { key: enabledKey, value: values.enabled },
    ])
  })
  const toggleEnabled = async (checked: boolean) => {
    if (form.formState.isSubmitting || updateOption.isPending) return
    form.setValue('enabled', checked, { shouldDirty: true })
    await submitForm(async (values) => {
      await updateOption.saveOptions([{ key: enabledKey, value: values.enabled }])
    })()
  }

  return {
    items: draft.items,
    setItems,
    parseError,
    isEnabled: form.watch('enabled'),
    hasChanges: form.formState.isDirty,
    isPending: form.formState.isSubmitting || updateOption.isPending,
    save,
    toggleEnabled,
  }
}

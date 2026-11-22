import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { handleBatchDeleteModels } from '../lib/model-actions'
import type { Model } from '../types'

export function ModelDeleteDialog({ models, onClose, onDeleted }: {
  models: Model[]
  onClose: () => void
  onDeleted?: (ids: number[]) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const [pending, setPending] = useState(false)
  const [removeChannels, setRemoveChannels] = useState(false)
  const [removePricing, setRemovePricing] = useState(false)
  const exact = models.every((model) => (model.name_rule ?? 0) === 0)
  const root = user?.role === 100
  const admin = user?.permissions?.sidebar_modules?.admin as
    | Record<string, unknown>
    | undefined
  const canRemoveChannels = exact && (root || (admin?.enabled === true && admin?.channel === true))
  const canRemovePricing = exact && root

  const submit = async () => {
    setPending(true)
    try {
      const complete = await handleBatchDeleteModels(
        models.map((model) => model.id),
        queryClient,
        onDeleted,
        {
          remove_from_channels: canRemoveChannels && removeChannels,
          remove_pricing: canRemovePricing && removePricing,
        }
      )
      if (complete) onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !pending) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('Delete Models?')}</DialogTitle>
          <DialogDescription>
            {t('Are you sure you want to delete {{count}} model(s)? This action cannot be undone.', { count: models.length })}
          </DialogDescription>
        </DialogHeader>
        <label className='flex items-center gap-2'>
          <Checkbox checked={canRemoveChannels && removeChannels} disabled={!canRemoveChannels || pending} onCheckedChange={(checked) => setRemoveChannels(checked === true)} />
          {t('Also remove exact matches from channels')}
        </label>
        <label className='flex items-center gap-2'>
          <Checkbox checked={canRemovePricing && removePricing} disabled={!canRemovePricing || pending} onCheckedChange={(checked) => setRemovePricing(checked === true)} />
          {t('Also remove exact pricing entries')}
        </label>
        <p className='text-muted-foreground text-sm'>
          {t('Linked deletion requires exact-match models and the corresponding permissions.')}
        </p>
        <DialogFooter>
          <Button variant='outline' disabled={pending} onClick={onClose}>{t('Cancel')}</Button>
          <Button variant='destructive' disabled={pending || !models.length} onClick={submit}>{t('Delete')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

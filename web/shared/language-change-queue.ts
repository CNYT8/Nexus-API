type LanguageChangeOptions = {
  getLanguage: () => string
  changeLanguage: (language: string) => Promise<unknown>
  saveLanguage: (language: string) => Promise<void>
  onConfirmed: (language: string) => void
  onError: (error: unknown) => void
  isCurrent: () => boolean
}

// Serialize both i18n loads and writes: a late load/rollback must never win over
// a newer selection. Only the latest queued selection needs to be started.
export function createLanguageChangeQueue(options: LanguageChangeOptions) {
  let confirmed = options.getLanguage()
  let queue = Promise.resolve()
  let latest = 0
  let pending = 0

  return (language: string): Promise<void> => {
    if (pending === 0) confirmed = options.getLanguage()
    const version = ++latest
    pending++
    const run = async () => {
      try {
        if (!options.isCurrent() || version !== latest) return
        try {
          await options.changeLanguage(language)
          if (!options.isCurrent()) return
          await options.saveLanguage(language)
          if (!options.isCurrent()) return
          confirmed = language
        } catch (error) {
          if (!options.isCurrent() || version !== latest) return
          try {
            await options.changeLanguage(confirmed)
          } catch {
            // A resource-load failure must not block the next queued selection.
          }
          if (options.isCurrent() && version === latest) {
            // An earlier, superseded write may have succeeded. Align the user
            // preference with that confirmed value after rolling back the UI.
            try { options.onConfirmed(confirmed) } catch { /* Report the original failure below. */ }
            options.onError(error)
          }
          return
        }
        if (options.isCurrent() && version === latest) {
          // Do not publish an intermediate preference: layout effects also
          // synchronize i18n from user settings and could race the next load.
          try { options.onConfirmed(confirmed) } catch (error) { options.onError(error) }
        }
      } finally {
        pending--
      }
    }
    queue = queue.then(run, run)
    return queue
  }
}

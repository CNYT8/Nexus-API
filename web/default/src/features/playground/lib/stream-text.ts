// Without a format marker, match upstream's prefix fallback. Identical chunks
// may be either repeated deltas or snapshots; that ambiguity cannot be resolved
// from text alone. Aliases in one event are alternatives, never concatenated.
export function appendStreamText(previous: string, incoming: unknown, snapshot = false): string {
  if (typeof incoming !== 'string' || !incoming) return ''
  if (snapshot && previous.startsWith(incoming)) return ''
  return previous && incoming.startsWith(previous)
    ? incoming.slice(previous.length)
    : incoming
}

export function createStreamTextAccumulator() {
  const accumulated = { reasoning: '', content: '' }
  return (value: unknown, snapshot = false) => {
    const chunk = (value ?? {}) as Record<string, unknown>
    const updates = { reasoning: '', content: '' }
    const text = {
      reasoning: typeof chunk.reasoning_content === 'string' && chunk.reasoning_content ? chunk.reasoning_content : chunk.reasoning,
      content: typeof chunk.content === 'string' && chunk.content ? chunk.content : chunk.text,
    }
    for (const field of ['reasoning', 'content'] as const) {
      const incoming = text[field]
      if (typeof incoming !== 'string') continue
      updates[field] = appendStreamText(accumulated[field], incoming, snapshot)
    }
    accumulated.reasoning += updates.reasoning
    accumulated.content += updates.content
    return updates
  }
}

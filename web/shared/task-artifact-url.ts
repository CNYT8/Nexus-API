export function taskArtifactContentUrl(
  value: unknown,
  taskId: unknown,
  artifactId: unknown
): string | undefined {
  if (
    typeof value !== 'string' ||
    typeof taskId !== 'string' ||
    typeof artifactId !== 'string' ||
    !taskId ||
    !artifactId
  ) {
    return undefined
  }

  try {
    const expected = `/api/task/self/${encodeURIComponent(taskId)}/artifacts/${encodeURIComponent(artifactId)}/content`
    if (value !== expected) return undefined

    // Reject dot segments that browsers normalize before sending the request.
    const parsed = new URL(expected, 'https://nexus.invalid')
    return parsed.pathname === expected ? expected : undefined
  } catch {
    return undefined
  }
}

/*
Copyright (C) 2025 QuantumNous

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

// Match upstream's prefix fallback when no format marker is present. Identical
// chunks can be repeated deltas or snapshots; text alone cannot distinguish them.
// Keep this behavior equivalent to default's stream-text.ts.
export function appendStreamText(previous, incoming, snapshot = false) {
  if (typeof incoming !== 'string' || !incoming) return '';
  if (snapshot && previous.startsWith(incoming)) return '';
  return previous && incoming.startsWith(previous)
    ? incoming.slice(previous.length)
    : incoming;
}

export function createStreamTextAccumulator() {
  const accumulated = { reasoning: '', content: '' };
  return (value, snapshot = false) => {
    const chunk = value ?? {};
    const updates = { reasoning: '', content: '' };
    const text = {
      reasoning: typeof chunk.reasoning_content === 'string' && chunk.reasoning_content ? chunk.reasoning_content : chunk.reasoning,
      content: typeof chunk.content === 'string' && chunk.content ? chunk.content : chunk.text,
    };
    for (const field of ['reasoning', 'content']) {
      const incoming = text[field];
      if (typeof incoming !== 'string') continue;
      updates[field] = appendStreamText(accumulated[field], incoming, snapshot);
    }
    accumulated.reasoning += updates.reasoning;
    accumulated.content += updates.content;
    return updates;
  };
}

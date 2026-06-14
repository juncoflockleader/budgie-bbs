/**
 * Markup — renders the BudgieBBS "markup" content type.
 * Supports: **bold**, `code`, > blockquote, bare URLs.
 */
import React from 'react'

interface Props {
  body: string
  redacted?: boolean
}

export function Markup({ body, redacted }: Props) {
  if (redacted) {
    return <span style={{ color: 'var(--c-muted)', fontStyle: 'italic' }}>[redacted]</span>
  }

  const lines = body.split('\n')
  return (
    <div className="markup">
      {lines.map((line, i) => {
        if (line.startsWith('> ')) {
          return (
            <blockquote key={i} className="markup-quote">
              {renderInline(line.slice(2))}
            </blockquote>
          )
        }
        return <p key={i} style={{ margin: '0.15em 0' }}>{renderInline(line)}</p>
      })}
    </div>
  )
}

function renderInline(text: string): React.ReactNode[] {
  // Split on **bold**, `code`, and URLs.
  const parts: React.ReactNode[] = []
  let rest = text
  let idx = 0

  const patterns: Array<{ re: RegExp; render: (m: RegExpMatchArray) => React.ReactNode }> = [
    { re: /\*\*(.+?)\*\*/, render: (m) => <strong key={idx++}>{m[1]}</strong> },
    { re: /`([^`]+)`/, render: (m) => <code key={idx++} className="markup-code">{m[1]}</code> },
    {
      re: /https?:\/\/[^\s)>\]]+/,
      render: (m) => (
        <a key={idx++} href={m[0]} target="_blank" rel="noreferrer" className="markup-link">
          {m[0]}
        </a>
      ),
    },
  ]

  while (rest.length > 0) {
    let earliest: { index: number; len: number; node: React.ReactNode } | null = null

    for (const { re, render } of patterns) {
      const m = rest.match(re)
      if (m && m.index !== undefined) {
        const candidate = { index: m.index, len: m[0].length, node: render(m) }
        if (!earliest || candidate.index < earliest.index) {
          earliest = candidate
        }
      }
    }

    if (!earliest) {
      parts.push(rest)
      break
    }

    if (earliest.index > 0) parts.push(rest.slice(0, earliest.index))
    parts.push(earliest.node)
    rest = rest.slice(earliest.index + earliest.len)
  }

  return parts
}

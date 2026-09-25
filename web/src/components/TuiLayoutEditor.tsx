import { useEffect, useState } from 'react'
import type { TUIMainMenuLayout, TUIBlock, TUIStockArt } from '../api/types'
import * as api from '../api/client'

const TYPES: NonNullable<TUIBlock['type']>[] = ['art', 'text', 'menu', 'spacer']
const ALIGNS: NonNullable<TUIBlock['align']>[] = ['left', 'center', 'right']

function defaultBlock(type: TUIBlock['type'], firstStock: string): TUIBlock {
  switch (type) {
    case 'art':
      return { type: 'art', stock: firstStock, align: 'center' }
    case 'text':
      return { type: 'text', text: '', align: 'center' }
    case 'spacer':
      return { type: 'spacer', lines: 1 }
    default:
      return { type: 'menu', align: 'left' }
  }
}

const typeLabel: Record<string, string> = {
  art: 'ASCII art', text: 'Text line', menu: 'Menu', spacer: 'Spacer',
}

// TuiLayoutEditor edits the SSH/TUI main-menu composition: an ordered stack of
// art / text / menu / spacer blocks. The menu block may appear at most once.
export function TuiLayoutEditor({ value, onChange }: {
  value: TUIMainMenuLayout
  onChange: (layout: TUIMainMenuLayout) => void
}) {
  const [stock, setStock] = useState<TUIStockArt[]>([])
  useEffect(() => {
    let cancelled = false
    api.getTUIStockArt().then(res => {
      if (!cancelled && res.data) setStock(res.data.arts)
    })
    return () => { cancelled = true }
  }, [])

  const blocks = value.blocks ?? []
  const firstStock = stock[0]?.name ?? 'budgie-bbs'
  const hasMenu = blocks.some(b => b.type === 'menu')
  const stockArt = (name?: string) => stock.find(s => s.name === name)?.art ?? ''

  const update = (next: TUIBlock[]) => onChange({ blocks: next })
  const setBlock = (i: number, patch: Partial<TUIBlock>) =>
    update(blocks.map((b, j) => (j === i ? { ...b, ...patch } : b)))
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir
    if (j < 0 || j >= blocks.length) return
    const next = blocks.slice()
    ;[next[i], next[j]] = [next[j], next[i]]
    update(next)
  }
  const remove = (i: number) => update(blocks.filter((_, j) => j !== i))
  const add = (type: TUIBlock['type']) => update([...blocks, defaultBlock(type, firstStock)])

  return (
    <div className="tui-layout-editor">
      <ol className="tui-block-list">
        {blocks.map((b, i) => {
          const isCustomArt = b.type === 'art' && !b.stock
          const preview = b.type === 'art' ? (b.stock ? stockArt(b.stock) : (b.art ?? '')) : ''
          return (
            <li key={i} className="tui-block">
              <div className="tui-block-head">
                <strong>{typeLabel[b.type ?? ''] ?? b.type}</strong>
                <span className="tui-block-actions">
                  <button type="button" className="link-btn" onClick={() => move(i, -1)} disabled={i === 0} aria-label="Move up">↑</button>
                  <button type="button" className="link-btn" onClick={() => move(i, 1)} disabled={i === blocks.length - 1} aria-label="Move down">↓</button>
                  <button type="button" className="link-btn" onClick={() => remove(i)} aria-label="Remove">✕</button>
                </span>
              </div>

              {b.type !== 'menu' && (
                <label className="tui-inline">
                  Align
                  <select value={b.align ?? 'left'} onChange={e => setBlock(i, { align: e.target.value as TUIBlock['align'] })}>
                    {ALIGNS.map(a => <option key={a} value={a}>{a}</option>)}
                  </select>
                </label>
              )}

              {b.type === 'art' && (
                <>
                  <label className="tui-inline">
                    Source
                    <select
                      value={b.stock ? b.stock : '__custom__'}
                      onChange={e => {
                        const v = e.target.value
                        if (v === '__custom__') setBlock(i, { stock: '', art: b.art ?? '' })
                        else setBlock(i, { stock: v, art: '' })
                      }}
                    >
                      {stock.map(s => <option key={s.name} value={s.name}>{s.name}</option>)}
                      <option value="__custom__">— custom —</option>
                    </select>
                  </label>
                  {isCustomArt && (
                    <textarea
                      className="tui-art-input"
                      rows={5}
                      spellCheck={false}
                      value={b.art ?? ''}
                      onChange={e => setBlock(i, { art: e.target.value })}
                      placeholder="Paste ASCII / ANSI art here"
                    />
                  )}
                  {preview && <pre className="tui-art-preview">{preview}</pre>}
                </>
              )}

              {b.type === 'text' && (
                <input
                  type="text"
                  maxLength={500}
                  value={b.text ?? ''}
                  onChange={e => setBlock(i, { text: e.target.value })}
                  placeholder="Leave blank to use the site tagline"
                />
              )}

              {b.type === 'spacer' && (
                <label className="tui-inline">
                  Blank lines
                  <input
                    type="number"
                    min={1}
                    max={12}
                    value={b.lines ?? 1}
                    onChange={e => setBlock(i, { lines: Math.max(1, Math.min(12, Number(e.target.value) || 1)) })}
                  />
                </label>
              )}

              {(b.type === 'art' || b.type === 'text') && (
                <label className="tui-inline">
                  Color
                  <span className="admin-color-row">
                    <input type="color" value={b.color || '#ffffff'} onChange={e => setBlock(i, { color: e.target.value })} />
                    {b.color && <button type="button" className="link-btn" onClick={() => setBlock(i, { color: '' })}>Clear</button>}
                  </span>
                </label>
              )}
            </li>
          )
        })}
      </ol>

      <div className="tui-block-add">
        {TYPES.filter(t => t !== 'menu' || !hasMenu).map(t => (
          <button key={t} type="button" className="link-btn" onClick={() => add(t)}>+ {typeLabel[t]}</button>
        ))}
      </div>
      {!hasMenu && <p className="muted">Add a Menu block so members can navigate (one is added automatically on save if omitted).</p>}
    </div>
  )
}

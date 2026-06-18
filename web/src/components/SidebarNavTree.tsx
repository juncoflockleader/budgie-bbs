import { useEffect, useState, type ReactElement } from 'react'
import * as api from '../api/client'
import type { Board, BoardSummary, Category, FavoriteFolder, FavoriteBoardEntry, FavoriteTree } from '../api/types'
import { useI18n } from '../i18n'

// Grouping helpers mirror BoardListPage's directory model: categories and boards
// share an id space, so a category node whose id matches a board is that board
// (a leaf); otherwise it is a sub-directory whose children are categories with
// parentId === its id.
function groupCategoriesByParent(categories: Category[]): Record<string, Category[]> {
  const ids = new Set(categories.map(c => c.id))
  const grouped: Record<string, Category[]> = {}
  categories.forEach(c => {
    const parent = c.parentId && ids.has(c.parentId) ? c.parentId : ''
    grouped[parent] = [...(grouped[parent] ?? []), c]
  })
  Object.values(grouped).forEach(items => items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name)))
  return grouped
}
function rootCategories(categories: Category[]): Category[] {
  const ids = new Set(categories.map(c => c.id))
  return categories.filter(c => !c.parentId || !ids.has(c.parentId)).slice()
    .sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
}
function indexBoardsById(boards: BoardSummary[]): Record<string, BoardSummary> {
  const out: Record<string, BoardSummary> = {}
  boards.forEach(b => { out[b.id] = b })
  return out
}
function groupFoldersByParent(folders: FavoriteFolder[]): Record<string, FavoriteFolder[]> {
  const grouped: Record<string, FavoriteFolder[]> = {}
  folders.forEach(f => { const p = f.parentId ?? ''; grouped[p] = [...(grouped[p] ?? []), f] })
  Object.values(grouped).forEach(items => items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name)))
  return grouped
}
function groupFavoritesByFolder(boards: FavoriteBoardEntry[]): Record<string, FavoriteBoardEntry[]> {
  const grouped: Record<string, FavoriteBoardEntry[]> = {}
  boards.forEach(b => { const f = b.folderId ?? ''; grouped[f] = [...(grouped[f] ?? []), b] })
  Object.values(grouped).forEach(items => items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name)))
  return grouped
}

interface Props {
  token: string
  activeBoardId?: string
  onOpenBoard: (board: Board) => void
}

// SidebarNavTree renders two collapsible trees in the sidebar: the user's
// favorite folders/boards, and the full category → board directory.
export function SidebarNavTree({ token, activeBoardId, onOpenBoard }: Props) {
  const { t } = useI18n()
  const [boards, setBoards] = useState<BoardSummary[]>([])
  const [categories, setCategories] = useState<Category[]>([])
  const [tree, setTree] = useState<FavoriteTree>({ folders: [], boards: [] })
  const [ready, setReady] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})

  useEffect(() => {
    let cancelled = false
    Promise.all([api.listBoardSummaries(token), api.listCategories(token), api.listFavoriteTree(token)]).then(([b, c, tr]) => {
      if (cancelled) return
      setBoards(b.data ?? [])
      setCategories(c.data ?? [])
      setTree(tr.data ?? { folders: [], boards: [] })
      setReady(true)
    })
    return () => { cancelled = true }
  }, [token])

  const toggle = (key: string) => setCollapsed(p => ({ ...p, [key]: !p[key] }))
  const isOpen = (key: string) => !collapsed[key]

  const boardsById = indexBoardsById(boards)
  const catByParent = groupCategoriesByParent(categories)
  const roots = rootCategories(categories)
  const categoryIdSet = new Set(categories.map(c => c.id))
  const uncategorized = boards.filter(b => !categoryIdSet.has(b.id)).slice().sort((a, b) => a.name.localeCompare(b.name))
  const foldersByParent = groupFoldersByParent(tree.folders)
  const favByFolder = groupFavoritesByFolder(tree.boards)

  function caret(key: string) {
    return <span className="nav-tree-caret">{isOpen(key) ? '▾' : '▸'}</span>
  }

  function boardRow(board: Board, depth: number, keyPrefix: string): ReactElement {
    const active = board.id === activeBoardId
    return (
      <button
        key={keyPrefix + board.id}
        className={`nav-tree-board${active ? ' nav-tree-board--active' : ''}`}
        style={{ paddingLeft: 10 + depth * 13 }}
        onClick={() => onOpenBoard(board)}
        title={board.description || board.name}
      >
        <span className="nav-tree-leaf">·</span>{board.name}
      </button>
    )
  }

  function categoryNode(cat: Category, depth: number): ReactElement {
    const board = boardsById[cat.id]
    if (board) return boardRow(board, depth, 'cb:')
    const children = catByParent[cat.id] ?? []
    const key = 'cat:' + cat.id
    return (
      <div key={key}>
        <button className="nav-tree-folder" style={{ paddingLeft: 6 + depth * 13 }} onClick={() => toggle(key)}>
          {caret(key)}{cat.name}
        </button>
        {isOpen(key) && children.map(ch => categoryNode(ch, depth + 1))}
      </div>
    )
  }

  function favFolderNode(folder: FavoriteFolder, depth: number): ReactElement {
    const childFolders = foldersByParent[folder.id] ?? []
    const childBoards = favByFolder[folder.id] ?? []
    const key = 'fav:' + folder.id
    return (
      <div key={key}>
        <button className="nav-tree-folder" style={{ paddingLeft: 6 + depth * 13 }} onClick={() => toggle(key)}>
          {caret(key)}{folder.name}
        </button>
        {isOpen(key) && (
          <>
            {childFolders.map(f => favFolderNode(f, depth + 1))}
            {childBoards.map(b => boardRow(b, depth + 1, 'fb:'))}
          </>
        )}
      </div>
    )
  }

  if (!ready) return null

  const rootFavFolders = foldersByParent[''] ?? []
  const rootFavBoards = favByFolder[''] ?? []
  const hasFavorites = rootFavFolders.length > 0 || rootFavBoards.length > 0

  return (
    <div className="nav-tree">
      {hasFavorites && (
        <>
          <button className="nav-tree-section" onClick={() => toggle('sec:fav')}>
            {caret('sec:fav')}<span className="nav-tree-section-icon">★</span>{t('board.favoritesSection')}
          </button>
          {isOpen('sec:fav') && (
            <div className="nav-tree-group">
              {rootFavFolders.map(f => favFolderNode(f, 1))}
              {rootFavBoards.map(b => boardRow(b, 1, 'fb:'))}
            </div>
          )}
        </>
      )}
      <button className="nav-tree-section" onClick={() => toggle('sec:boards')}>
        {caret('sec:boards')}<span className="nav-tree-section-icon">⊞</span>{t('nav.boards')}
      </button>
      {isOpen('sec:boards') && (
        <div className="nav-tree-group">
          {roots.map(r => categoryNode(r, 1))}
          {uncategorized.map(b => boardRow(b, 1, 'ub:'))}
        </div>
      )}
    </div>
  )
}

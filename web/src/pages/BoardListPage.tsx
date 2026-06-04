import { type MouseEvent, useEffect, useState } from 'react'
import * as api from '../api/client'
import type { Board, BoardSummary, Category, FavoriteBoardEntry, FavoriteFolder, FavoriteTree } from '../api/types'
import { Spinner } from '../components/Spinner'

type BoardSortMode = 'name' | 'new' | 'online' | 'posts' | 'activity' | 'unread'

interface Props {
  token: string
  currentUserRole: string
  onSelect: (board: Board) => void
}

export function BoardListPage({ token, currentUserRole, onSelect }: Props) {
  const [boards, setBoards] = useState<BoardSummary[]>([])
  const [categories, setCategories] = useState<Category[]>([])
  const [favoriteTree, setFavoriteTree] = useState<FavoriteTree>({ folders: [], boards: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [boardQuery, setBoardQuery] = useState('')
  const [boardSort, setBoardSort] = useState<BoardSortMode>('name')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    Promise.all([api.listBoardSummaries(token), api.listFavoriteTree(token), api.listCategories(token)]).then(([boardsRes, treeRes, categoriesRes]) => {
      if (cancelled) return
      setLoading(false)
      if (boardsRes.error) {
        setError(boardsRes.error.message)
        return
      }
      if (treeRes.error) {
        setError(treeRes.error.message)
        return
      }
      if (categoriesRes.error) {
        setError(categoriesRes.error.message)
        return
      }
      setBoards(boardsRes.data ?? [])
      setFavoriteTree(treeRes.data ?? { folders: [], boards: [] })
      setCategories(categoriesRes.data ?? [])
    })
    return () => {
      cancelled = true
    }
  }, [token])

  if (loading) return <Spinner />
  if (error) return <p className="error">{error}</p>

  const visibleBoards = sortBoards(boards.filter(board => matchesBoardSearch(board, boardQuery)), boardSort)
  const unreadBoards = visibleBoards.filter(board => board.unreadPosts > 0)
  const newBoards = visibleBoards.filter(board => board.newBoard)
  const boardsById = indexBoardsById(visibleBoards)
  const categoriesByParent = groupCategoriesByParent(categories)
  const categoryIds = new Set(categories.map(category => category.id))
  const directoryRoots = rootCategories(categories)
  const uncategorizedBoards = visibleBoards.filter(board => !categoryIds.has(board.id))
  const foldersByParent = groupFoldersByParent(favoriteTree.folders)
  const favoritesByFolder = groupFavoritesByFolder(favoriteTree.boards.filter(board => matchesBoardSearch(board, boardQuery)))
  const folderOptions = favoriteTree.folders
    .slice()
    .sort((a, b) => a.name.localeCompare(b.name))
  const isAdmin = currentUserRole === 'admin'

  async function reloadBoards(previousBoards = boards, previousTree = favoriteTree) {
    const [boardsRes, treeRes, categoriesRes] = await Promise.all([api.listBoardSummaries(token), api.listFavoriteTree(token), api.listCategories(token)])
    if (boardsRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setError(boardsRes.error.message)
      return
    }
    if (treeRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setError(treeRes.error.message)
      return
    }
    if (categoriesRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setError(categoriesRes.error.message)
      return
    }
    setBoards(boardsRes.data ?? [])
    setFavoriteTree(treeRes.data ?? { folders: [], boards: [] })
    setCategories(categoriesRes.data ?? [])
  }

  async function toggleFavorite(board: BoardSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const wasFavorite = board.favorite
    const res = await api.setBoardFavorite(token, board.id, !wasFavorite)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function markRead(board: BoardSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const previous = boards
    setBoards(current => current.map(item => (item.id === board.id ? { ...item, unreadPosts: 0, unreadThreads: 0, readSeq: board.lastSeq } : item)))
    const res = await api.markBoardRead(token, board.id)
    if (res.error) {
      setBoards(previous)
      setError(res.error.message)
      return
    }
    await reloadBoards(previous)
  }

  async function restoreRead(board: BoardSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const previous = boards
    const res = await api.restoreBoardRead(token, board.id)
    if (res.error) {
      setBoards(previous)
      setError(res.error.message)
      return
    }
    await reloadBoards(previous)
  }

  async function markFavoriteScopeRead(folderId: string, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const res = await api.markFavoriteFolderRead(token, folderId)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function restoreFavoriteScopeRead(folderId: string, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const res = await api.restoreFavoriteFolderRead(token, folderId)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function exportFavorites(e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const res = await api.exportFavoriteTree(token)
    if (res.error || !res.data) {
      setError(res.error?.message ?? 'Unable to export favorites')
      return
    }
    const blob = new Blob([JSON.stringify({ ...res.data, exportedAt: new Date().toISOString() }, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'budgie-favorites.json'
    link.click()
    URL.revokeObjectURL(url)
  }

  async function importFavorites(e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const input = document.createElement('input')
    input.type = 'file'
    input.accept = 'application/json,.json'
    input.onchange = () => {
      const file = input.files?.[0]
      if (!file) return
      const reader = new FileReader()
      reader.onload = () => {
        try {
          const parsed = JSON.parse(String(reader.result))
          if (!Array.isArray(parsed.folders) || !Array.isArray(parsed.boards)) {
            setError('Favorite import must contain folders and boards arrays')
            return
          }
          api.importFavoriteTree(token, { folders: parsed.folders, boards: parsed.boards, replace: true }).then(res => {
            if (res.error) {
              setError(res.error.message)
              return
            }
            void reloadBoards()
          })
        } catch (err) {
          setError(err instanceof Error ? err.message : 'Invalid favorite export JSON')
        }
      }
      reader.readAsText(file)
    }
    input.click()
  }

  async function applyMembership(board: BoardSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const note = prompt('Application note:', '')
    if (note === null) return
    const res = await api.applyBoardMembership(token, board.id, note)
    if (res.error) {
      setError(res.error.message)
      return
    }
    setError(null)
  }

  async function createFolder(parentId = '', e?: MouseEvent<HTMLButtonElement>) {
    e?.stopPropagation()
    const name = prompt('Folder name:')
    if (!name?.trim()) return
    const res = await api.createFavoriteFolder(token, name.trim(), parentId)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function renameFolder(folder: FavoriteFolder, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const name = prompt('Folder name:', folder.name)
    if (!name?.trim()) return
    const res = await api.updateFavoriteFolder(token, folder.id, { name: name.trim() })
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function deleteFolder(folder: FavoriteFolder, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    if (!confirm(`Delete "${folder.name}"? Its boards and subfolders move up one level.`)) return
    const res = await api.deleteFavoriteFolder(token, folder.id)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function moveFolder(folder: FavoriteFolder, direction: 'up' | 'down', e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const siblings = foldersByParent[folder.parentId ?? ''] ?? []
    const index = siblings.findIndex(item => item.id === folder.id)
    const target = direction === 'up' ? siblings[index - 1] : siblings[index + 1]
    if (!target) return
    const position = direction === 'up' ? target.position : target.position + 1
    const res = await api.updateFavoriteFolder(token, folder.id, { position })
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function moveFavoriteBoard(board: FavoriteBoardEntry, folderId: string, position?: number) {
    const res = await api.moveBoardFavorite(token, board.id, folderId, position)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function reorderFavoriteBoard(board: FavoriteBoardEntry, direction: 'up' | 'down', e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const folderId = board.folderId ?? ''
    const siblings = favoritesByFolder[folderId] ?? []
    const index = siblings.findIndex(item => item.id === board.id)
    const target = direction === 'up' ? siblings[index - 1] : siblings[index + 1]
    if (!target) return
    const position = direction === 'up' ? target.position : target.position + 1
    await moveFavoriteBoard(board, folderId, position)
  }

  async function updateCategory(category: Category, patch: { name?: string; description?: string; parentId?: string; position?: number; visibility?: string }) {
    const res = await api.updateCategory(token, category.id, patch)
    if (res.error) {
      setError(res.error.message)
      return
    }
    await reloadBoards()
  }

  async function renameCategory(category: Category, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const name = prompt('Category name:', category.name)
    if (!name?.trim()) return
    const description = prompt('Description:', category.description)
    await updateCategory(category, { name: name.trim(), description: description ?? category.description })
  }

  async function reparentCategory(category: Category, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const parentId = prompt('Parent category id:', category.parentId ?? '')
    if (parentId === null) return
    await updateCategory(category, { parentId: parentId.trim() })
  }

  async function moveCategory(category: Category, direction: 'up' | 'down', e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const parent = category.parentId && categoryIds.has(category.parentId) ? category.parentId : ''
    const siblings = categoriesByParent[parent] ?? []
    const index = siblings.findIndex(item => item.id === category.id)
    const target = direction === 'up' ? siblings[index - 1] : siblings[index + 1]
    if (!target) return
    const position = direction === 'up' ? target.position : target.position + 1
    await updateCategory(category, { position })
  }

  function renderBoard(board: BoardSummary) {
    const isFavorite = board.favorite
    return (
      <li key={board.id} className="item-row board-row" onClick={() => onSelect(board)}>
        <span className="board-row-content">
          <span className="item-title">{board.name}</span>
          {board.description && <span className="item-desc muted">{board.description}</span>}
          <BoardStats board={board} />
          <PolicyBadges board={board} />
          {board.unreadPosts > 0 && (
            <span className="item-meta unread-meta">
              {board.unreadPosts} unread post{board.unreadPosts === 1 ? '' : 's'}
              {board.unreadThreads > 0 && ` across ${board.unreadThreads} thread${board.unreadThreads === 1 ? '' : 's'}`}
            </span>
          )}
        </span>
        <span className="board-row-actions">
          {board.unreadPosts > 0 && (
            <button
              className="board-action-btn"
              onClick={e => markRead(board, e)}
              title="Mark board read"
              aria-label={`Mark ${board.name} read`}
            >
              ✓
            </button>
          )}
          {board.readSeq > 0 && (
            <button
              className="board-action-btn"
              onClick={e => restoreRead(board, e)}
              title="Restore read marker"
              aria-label={`Restore ${board.name} read marker`}
            >
              ↶
            </button>
          )}
          {(board.memberReadMode || board.memberPostMode) && (
            <button
              className="board-action-btn"
              onClick={e => applyMembership(board, e)}
              title="Apply for membership"
              aria-label={`Apply to join ${board.name}`}
            >
              Apply
            </button>
          )}
          <button
            className={`favorite-btn${isFavorite ? ' favorite-btn--active' : ''}`}
            onClick={e => toggleFavorite(board, e)}
            title={isFavorite ? 'Remove favorite' : 'Add favorite'}
            aria-label={isFavorite ? `Remove ${board.name} from favorites` : `Add ${board.name} to favorites`}
          >
            {isFavorite ? '★' : '☆'}
          </button>
        </span>
      </li>
    )
  }

  function renderFavoriteBoard(board: FavoriteBoardEntry) {
    const summary = favoriteEntryToSummary(board)
    const siblings = favoritesByFolder[board.folderId ?? ''] ?? []
    const index = siblings.findIndex(item => item.id === board.id)
    return (
      <li key={board.id} className="item-row board-row favorite-board-row" onClick={() => onSelect(summary)}>
        <span className="board-row-content">
          <span className="item-title">{board.name}</span>
          {board.description && <span className="item-desc muted">{board.description}</span>}
          <PolicyBadges board={summary} />
          {board.unreadPosts > 0 && (
            <span className="item-meta unread-meta">
              {board.unreadPosts} unread post{board.unreadPosts === 1 ? '' : 's'}
              {board.unreadThreads > 0 && ` across ${board.unreadThreads} thread${board.unreadThreads === 1 ? '' : 's'}`}
            </span>
          )}
        </span>
        <span className="board-row-actions">
          <select
            className="favorite-folder-select"
            value={board.folderId ?? ''}
            onClick={e => e.stopPropagation()}
            onChange={e => { void moveFavoriteBoard(board, e.currentTarget.value) }}
            aria-label={`Move ${board.name} to favorite folder`}
          >
            <option value="">Root</option>
            {folderOptions.map(folder => (
              <option key={folder.id} value={folder.id}>{folder.name}</option>
            ))}
          </select>
          <button className="board-action-btn" disabled={index <= 0} onClick={e => reorderFavoriteBoard(board, 'up', e)} title="Move up">↑</button>
          <button className="board-action-btn" disabled={index < 0 || index >= siblings.length - 1} onClick={e => reorderFavoriteBoard(board, 'down', e)} title="Move down">↓</button>
          {summary.unreadPosts > 0 && (
            <button className="board-action-btn" onClick={e => markRead(summary, e)} title="Mark board read" aria-label={`Mark ${summary.name} read`}>✓</button>
          )}
          {summary.readSeq > 0 && (
            <button className="board-action-btn" onClick={e => restoreRead(summary, e)} title="Restore read marker" aria-label={`Restore ${summary.name} read marker`}>↶</button>
          )}
          <button className="favorite-btn favorite-btn--active" onClick={e => toggleFavorite(summary, e)} title="Remove favorite" aria-label={`Remove ${summary.name} from favorites`}>★</button>
        </span>
      </li>
    )
  }

  function renderFavoriteFolder(folder: FavoriteFolder, depth = 0) {
    const childFolders = foldersByParent[folder.id] ?? []
    const folderBoards = favoritesByFolder[folder.id] ?? []
    const siblings = foldersByParent[folder.parentId ?? ''] ?? []
    const index = siblings.findIndex(item => item.id === folder.id)
    const stats = favoriteScopeStats(folder.id, foldersByParent, favoritesByFolder)
    return (
      <div key={folder.id} className="favorite-folder" style={{ marginLeft: `${depth * 0.75}rem` }}>
        <div className="favorite-folder-header">
          <span className="favorite-folder-name">{folder.name}</span>
          <span className="favorite-folder-actions">
            {stats.unreadPosts > 0 && <button className="board-action-btn" onClick={e => markFavoriteScopeRead(folder.id, e)} title="Mark folder read">✓</button>}
            {stats.hasReadMarker && <button className="board-action-btn" onClick={e => restoreFavoriteScopeRead(folder.id, e)} title="Restore folder read markers">↶</button>}
            <button className="board-action-btn" onClick={e => createFolder(folder.id, e)} title="New nested folder">＋</button>
            <button className="board-action-btn" onClick={e => renameFolder(folder, e)} title="Rename folder">✎</button>
            <button className="board-action-btn" disabled={index <= 0} onClick={e => moveFolder(folder, 'up', e)} title="Move folder up">↑</button>
            <button className="board-action-btn" disabled={index < 0 || index >= siblings.length - 1} onClick={e => moveFolder(folder, 'down', e)} title="Move folder down">↓</button>
            <button className="board-action-btn" onClick={e => deleteFolder(folder, e)} title="Delete folder">×</button>
          </span>
        </div>
        {folderBoards.length > 0 && <ul className="item-list favorite-folder-list">{folderBoards.map(renderFavoriteBoard)}</ul>}
        {childFolders.map(child => renderFavoriteFolder(child, depth + 1))}
      </div>
    )
  }

  function renderCategory(category: Category, depth = 0) {
    const board = boardsById[category.id]
    const children = categoriesByParent[category.id] ?? []
    const renderedChildren = children.map(child => renderCategory(child, depth + 1))
    if (!board && renderedChildren.every(child => child === null)) return null
    const parent = category.parentId && categoryIds.has(category.parentId) ? category.parentId : ''
    const siblings = categoriesByParent[parent] ?? []
    const index = siblings.findIndex(item => item.id === category.id)
    const controls = isAdmin ? (
      <span className="category-admin-actions">
        <button className="board-action-btn" disabled={index <= 0} onClick={e => moveCategory(category, 'up', e)} title="Move category up">↑</button>
        <button className="board-action-btn" disabled={index < 0 || index >= siblings.length - 1} onClick={e => moveCategory(category, 'down', e)} title="Move category down">↓</button>
        <button className="board-action-btn" onClick={e => renameCategory(category, e)} title="Edit category">Edit</button>
        <button className="board-action-btn" onClick={e => reparentCategory(category, e)} title="Move category">Move</button>
        <select
          className="favorite-folder-select"
          value={category.visibility || 'public'}
          onClick={e => e.stopPropagation()}
          onChange={e => updateCategory(category, { visibility: e.target.value })}
          title="Directory visibility"
        >
          <option value="public">Public</option>
          <option value="staff">Staff</option>
          <option value="hidden">Hidden</option>
        </select>
      </span>
    ) : null
    return (
      <div className="category-branch" key={category.id} style={{ marginLeft: `${depth * 0.85}rem` }}>
        {board ? (
          <div className="category-board-entry">
            <ul className="item-list category-board-list">{renderBoard(board)}</ul>
            {controls}
          </div>
        ) : (
          <div className="category-heading">
            <span className="category-heading-main">
              <span className="item-title">{category.name}</span>
              {category.description && <span className="item-desc muted">{category.description}</span>}
            </span>
            {controls}
          </div>
        )}
        {renderedChildren}
      </div>
    )
  }

  const favoriteStats = favoriteScopeStats('', foldersByParent, favoritesByFolder)

  return (
    <div className="board-list">
      <h2>Boards</h2>
      <div className="board-discovery-controls">
        <input
          value={boardQuery}
          onChange={e => setBoardQuery(e.target.value)}
          placeholder="Search boards"
          aria-label="Search boards"
        />
        <select value={boardSort} onChange={e => setBoardSort(e.target.value as BoardSortMode)} aria-label="Sort boards">
          <option value="name">Name</option>
          <option value="new">New</option>
          <option value="online">Online</option>
          <option value="posts">Articles</option>
          <option value="activity">Activity</option>
          <option value="unread">Unread</option>
        </select>
      </div>
      <section className="board-section">
        <h3 className="board-section-title">Unread</h3>
        {unreadBoards.length === 0 && <p className="muted">No unread boards.</p>}
        {unreadBoards.length > 0 && (
          <ul className="item-list">
            {unreadBoards.map(renderBoard)}
          </ul>
        )}
      </section>
      <section className="board-section">
        <h3 className="board-section-title">New Boards</h3>
        {newBoards.length === 0 && <p className="muted">No new boards.</p>}
        {newBoards.length > 0 && (
          <ul className="item-list">
            {newBoards.map(renderBoard)}
          </ul>
        )}
      </section>
      <section className="board-section">
        <div className="board-section-heading">
          <h3 className="board-section-title">Favorites</h3>
          <span className="favorite-folder-actions">
            {favoriteStats.unreadPosts > 0 && <button className="board-action-btn" onClick={e => markFavoriteScopeRead('', e)} title="Mark all favorites read">✓</button>}
            {favoriteStats.hasReadMarker && <button className="board-action-btn" onClick={e => restoreFavoriteScopeRead('', e)} title="Restore favorite read markers">↶</button>}
            <button className="board-action-btn" onClick={exportFavorites} title="Export favorites">Export</button>
            <button className="board-action-btn" onClick={importFavorites} title="Import favorites">Import</button>
            <button className="board-action-btn" onClick={e => createFolder('', e)} title="New favorite folder">＋</button>
          </span>
        </div>
        {favoriteTree.folders.length === 0 && favoriteTree.boards.length === 0 && <p className="muted">No favorite boards yet.</p>}
        {(favoritesByFolder['']?.length ?? 0) > 0 && (
          <ul className="item-list favorite-folder-list">
            {(favoritesByFolder[''] ?? []).map(renderFavoriteBoard)}
          </ul>
        )}
        {(foldersByParent[''] ?? []).map(folder => renderFavoriteFolder(folder))}
      </section>
      <section className="board-section">
        <h3 className="board-section-title">Directory</h3>
        {directoryRoots.length === 0 && visibleBoards.length === 0 && <p className="muted">No boards yet.</p>}
        {directoryRoots.map(category => renderCategory(category))}
        {uncategorizedBoards.length > 0 && (
          <ul className="item-list">
            {uncategorizedBoards.map(renderBoard)}
          </ul>
        )}
      </section>
      <section className="board-section">
        <h3 className="board-section-title">All Boards</h3>
        {visibleBoards.length === 0 && <p className="muted">No boards yet.</p>}
        {visibleBoards.length > 0 && (
          <ul className="item-list">
            {visibleBoards.map(renderBoard)}
          </ul>
        )}
      </section>
    </div>
  )
}

function groupFoldersByParent(folders: FavoriteFolder[]) {
  const grouped: Record<string, FavoriteFolder[]> = {}
  folders.forEach(folder => {
    const parent = folder.parentId ?? ''
    grouped[parent] = [...(grouped[parent] ?? []), folder]
  })
  Object.values(grouped).forEach(items => {
    items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
  })
  return grouped
}

function groupCategoriesByParent(categories: Category[]) {
  const ids = new Set(categories.map(category => category.id))
  const grouped: Record<string, Category[]> = {}
  categories.forEach(category => {
    const parent = category.parentId && ids.has(category.parentId) ? category.parentId : ''
    grouped[parent] = [...(grouped[parent] ?? []), category]
  })
  Object.values(grouped).forEach(items => {
    items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
  })
  return grouped
}

function rootCategories(categories: Category[]) {
  const ids = new Set(categories.map(category => category.id))
  return categories
    .filter(category => !category.parentId || !ids.has(category.parentId))
    .slice()
    .sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
}

function matchesBoardSearch(board: Pick<Board, 'id' | 'name' | 'description'>, query: string) {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return true
  return [board.id, board.name, board.description].some(value => value.toLocaleLowerCase().includes(needle))
}

function sortBoards(boards: BoardSummary[], mode: BoardSortMode) {
  const copy = boards.slice()
  const byName = (a: BoardSummary, b: BoardSummary) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id)
  copy.sort((a, b) => {
    switch (mode) {
      case 'new':
        return b.createdAt - a.createdAt || byName(a, b)
      case 'online':
        return b.onlineUsers - a.onlineUsers || byName(a, b)
      case 'posts':
        return b.postCount - a.postCount || b.threadCount - a.threadCount || byName(a, b)
      case 'activity':
        return b.lastSeq - a.lastSeq || byName(a, b)
      case 'unread':
        return b.unreadPosts - a.unreadPosts || b.unreadThreads - a.unreadThreads || byName(a, b)
      case 'name':
      default:
        return (Number(b.favorite) - Number(a.favorite)) || byName(a, b)
    }
  })
  return copy
}

function indexBoardsById(boards: BoardSummary[]) {
  const out: Record<string, BoardSummary> = {}
  boards.forEach(board => { out[board.id] = board })
  return out
}

function groupFavoritesByFolder(boards: FavoriteBoardEntry[]) {
  const grouped: Record<string, FavoriteBoardEntry[]> = {}
  boards.forEach(board => {
    const folder = board.folderId ?? ''
    grouped[folder] = [...(grouped[folder] ?? []), board]
  })
  Object.values(grouped).forEach(items => {
    items.sort((a, b) => a.position - b.position || a.name.localeCompare(b.name))
  })
  return grouped
}

function favoriteScopeStats(
  folderId: string,
  foldersByParent: Record<string, FavoriteFolder[]>,
  favoritesByFolder: Record<string, FavoriteBoardEntry[]>,
) {
  const boards = favoritesByFolder[folderId] ?? []
  let unreadPosts = boards.reduce((sum, board) => sum + board.unreadPosts, 0)
  let hasReadMarker = boards.some(board => board.readSeq > 0)
  for (const child of foldersByParent[folderId] ?? []) {
    const childStats = favoriteScopeStats(child.id, foldersByParent, favoritesByFolder)
    unreadPosts += childStats.unreadPosts
    hasReadMarker = hasReadMarker || childStats.hasReadMarker
  }
  return { unreadPosts, hasReadMarker }
}

function favoriteEntryToSummary(board: FavoriteBoardEntry): BoardSummary {
  return {
    id: board.id,
    name: board.name,
    description: board.description,
    favorite: true,
    unreadThreads: board.unreadThreads,
    unreadPosts: board.unreadPosts,
    threadCount: 0,
    postCount: 0,
    onlineUsers: 0,
    lastSeq: board.lastSeq,
    readSeq: board.readSeq,
    createdAt: 0,
    newBoard: false,
    anonymousAllowed: false,
    readOnly: false,
    noReply: false,
    attachmentsAllowed: false,
    mailInAllowed: false,
    relayEnabled: false,
    memberReadMode: false,
    memberPostMode: false,
    moderatorCount: 0,
  }
}

function BoardStats({ board }: { board: BoardSummary }) {
  const parts = [
    board.newBoard && 'New',
    `${board.postCount} article${board.postCount === 1 ? '' : 's'}`,
    `${board.threadCount} thread${board.threadCount === 1 ? '' : 's'}`,
    `${board.onlineUsers} online`,
  ].filter(Boolean)
  return <span className="item-meta board-stat-meta muted">{parts.join(' / ')}</span>
}

function PolicyBadges({ board }: { board: BoardSummary }) {
  const badges = [
    board.readOnly && 'Read-only',
    board.noReply && 'No replies',
    board.anonymousAllowed && 'Anonymous',
    board.attachmentsAllowed && 'Attachments',
    board.mailInAllowed && 'Mail-in',
    board.relayEnabled && 'Relay',
    board.memberReadMode && 'Member read',
    board.memberPostMode && 'Member post',
    board.moderatorCount > 0 && `${board.moderatorCount} mod${board.moderatorCount === 1 ? '' : 's'}`,
  ].filter(Boolean)
  if (badges.length === 0) return null
  return (
    <span className="policy-badge-row">
      {badges.map(badge => <span key={String(badge)} className="policy-badge">{badge}</span>)}
    </span>
  )
}

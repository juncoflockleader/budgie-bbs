import { type MouseEvent, useEffect, useState } from 'react'
import * as api from '../api/client'
import type { Board, BoardSummary, Category, FavoriteBoardEntry, FavoriteFolder, FavoriteTree, RecommendedBoard } from '../api/types'
import { Spinner } from '../components/Spinner'
import { useI18n } from '../i18n'

type BoardSortMode = 'name' | 'new' | 'online' | 'posts' | 'activity' | 'unread'

interface Props {
  token: string
  currentUserRole: string
  onSelect: (board: Board) => void
}

type BoardTab = 'unread' | 'favorites' | 'directory' | 'all'

function savedTab(): BoardTab {
  try { return (localStorage.getItem('budgieBoardTab') as BoardTab) ?? 'unread' } catch { return 'unread' }
}

export function BoardListPage({ token, currentUserRole, onSelect }: Props) {
  const { t } = useI18n()
  const [boards, setBoards] = useState<BoardSummary[]>([])
  const [recommendedBoards, setRecommendedBoards] = useState<RecommendedBoard[]>([])
  const [categories, setCategories] = useState<Category[]>([])
  const [favoriteTree, setFavoriteTree] = useState<FavoriteTree>({ folders: [], boards: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [boardQuery, setBoardQuery] = useState('')
  const [boardSort, setBoardSort] = useState<BoardSortMode>('name')
  const [activeTab, setActiveTab] = useState<BoardTab>(savedTab)

  function switchTab(tab: BoardTab) {
    setActiveTab(tab)
    try { localStorage.setItem('budgieBoardTab', tab) } catch { /* ignore */ }
  }

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    Promise.all([api.listBoardSummaries(token), api.listRecommendedBoards(token), api.listFavoriteTree(token), api.listCategories(token)]).then(([boardsRes, recommendedRes, treeRes, categoriesRes]) => {
      if (cancelled) return
      setLoading(false)
      if (boardsRes.error) {
        setError(boardsRes.error.message)
        return
      }
      if (recommendedRes.error) {
        setError(recommendedRes.error.message)
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
      setRecommendedBoards(recommendedRes.data ?? [])
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
  const unreadBoards = visibleBoards.filter(board => board.unreadPosts > 0 && !board.zapped)
  const newBoards = visibleBoards.filter(board => board.newBoard)
  const recommendedVisibleBoards = sortRecommendedBoards(recommendedBoards.filter(board => matchesBoardSearch(board, boardQuery)))
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

  function plural(count: number) {
    return count === 1 ? '' : 's'
  }

  function unreadSummary(postCount: number, threadCount: number) {
    const posts = t('board.postCount', { count: postCount, plural: plural(postCount) })
    if (threadCount === 0) return posts
    return `${posts}${t('board.across')} ${t('board.unreadThreads', { count: threadCount, plural: plural(threadCount) })}`
  }

  async function reloadBoards(previousBoards = boards, previousTree = favoriteTree, previousRecommended = recommendedBoards) {
    const [boardsRes, recommendedRes, treeRes, categoriesRes] = await Promise.all([api.listBoardSummaries(token), api.listRecommendedBoards(token), api.listFavoriteTree(token), api.listCategories(token)])
    if (boardsRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setRecommendedBoards(previousRecommended)
      setError(boardsRes.error.message)
      return
    }
    if (recommendedRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setRecommendedBoards(previousRecommended)
      setError(recommendedRes.error.message)
      return
    }
    if (treeRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setRecommendedBoards(previousRecommended)
      setError(treeRes.error.message)
      return
    }
    if (categoriesRes.error) {
      setBoards(previousBoards)
      setFavoriteTree(previousTree)
      setRecommendedBoards(previousRecommended)
      setError(categoriesRes.error.message)
      return
    }
    setBoards(boardsRes.data ?? [])
    setRecommendedBoards(recommendedRes.data ?? [])
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

  async function toggleZap(board: BoardSummary, e: MouseEvent<HTMLButtonElement>) {
    e.stopPropagation()
    const res = await api.setBoardZap(token, board.id, !board.zapped)
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
          {!board.zapped && board.unreadPosts > 0 && (
            <span className="item-meta unread-meta">
              {unreadSummary(board.unreadPosts, board.unreadThreads)}
            </span>
          )}
        </span>
        <span className="board-row-actions">
          {board.unreadPosts > 0 && (
            <button
              className="board-action-btn"
              onClick={e => markRead(board, e)}
              title={t('board.boardActions.markRead')}
              aria-label={t('board.actionMarkRead', { board: board.name })}
            >
              ✓
            </button>
          )}
          {board.readSeq > 0 && (
            <button
              className="board-action-btn"
              onClick={e => restoreRead(board, e)}
              title={t('board.boardActions.restoreReadMarker')}
              aria-label={t('board.actionRestoreReadMarker', { board: board.name })}
            >
              ↶
            </button>
          )}
          {(board.memberReadMode || board.memberPostMode) && (
            <button
              className="board-action-btn"
              onClick={e => applyMembership(board, e)}
              title={t('board.apply')}
              aria-label={t('board.applyToJoin', { board: board.name })}
            >
              {t('board.apply')}
            </button>
          )}
          {board.zapAllowed && (
            <button
              className={`board-action-btn${board.zapped ? ' favorite-btn--active' : ''}`}
              onClick={e => toggleZap(board, e)}
              title={board.zapped ? t('board.boardActions.showInUnread') : t('board.boardActions.hideFromUnread')}
              aria-label={board.zapped
                ? t('board.actionShowInUnread', { board: board.name })
                : t('board.actionHideFromUnread', { board: board.name })}
            >
              {t('board.zap')}
            </button>
          )}
          <button
            className={`favorite-btn${isFavorite ? ' favorite-btn--active' : ''}`}
            onClick={e => toggleFavorite(board, e)}
            title={isFavorite ? t('board.boardActions.removeFavorite') : t('board.boardActions.addFavorite')}
            aria-label={isFavorite ? t('board.actionRemoveFavorite', { board: board.name }) : t('board.actionAddFavorite', { board: board.name })}
          >
            {isFavorite ? '★' : '☆'}
          </button>
        </span>
      </li>
    )
  }

  function renderFavoriteBoard(board: FavoriteBoardEntry) {
    const summary = favoriteEntryToSummary(board, boardsById[board.id])
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
              {unreadSummary(board.unreadPosts, board.unreadThreads)}
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
            <option value="">{t('board.root')}</option>
            {folderOptions.map(folder => (
              <option key={folder.id} value={folder.id}>{folder.name}</option>
            ))}
          </select>
          <button className="board-action-btn" disabled={index <= 0} onClick={e => reorderFavoriteBoard(board, 'up', e)} title={t('board.favoriteActions.moveCategoryUp')}>↑</button>
          <button className="board-action-btn" disabled={index < 0 || index >= siblings.length - 1} onClick={e => reorderFavoriteBoard(board, 'down', e)} title={t('board.favoriteActions.moveCategoryDown')}>↓</button>
          {!summary.zapped && summary.unreadPosts > 0 && (
            <button className="board-action-btn" onClick={e => markRead(summary, e)} title={t('board.boardActions.markRead')} aria-label={t('board.actionMarkRead', { board: summary.name })}>✓</button>
          )}
          {summary.readSeq > 0 && (
            <button className="board-action-btn" onClick={e => restoreRead(summary, e)} title={t('board.boardActions.restoreReadMarker')} aria-label={t('board.actionRestoreReadMarker', { board: summary.name })}>↶</button>
          )}
          {summary.zapAllowed && (
            <button
              className={`board-action-btn${summary.zapped ? ' favorite-btn--active' : ''}`}
              onClick={e => toggleZap(summary, e)}
              title={summary.zapped ? t('board.boardActions.showInUnread') : t('board.boardActions.hideFromUnread')}
              aria-label={summary.zapped
                ? t('board.actionShowInUnread', { board: summary.name })
                : t('board.actionHideFromUnread', { board: summary.name })}
            >
              {t('board.zap')}
            </button>
          )}
          <button className="favorite-btn favorite-btn--active" onClick={e => toggleFavorite(summary, e)} title={t('board.boardActions.removeFavorite')} aria-label={t('board.actionRemoveFavorite', { board: summary.name })}>★</button>
        </span>
      </li>
    )
  }

  function renderRecommendedBoard(board: RecommendedBoard) {
    const summary = recommendedBoardToSummary(board, boardsById[board.id])
    return (
      <li key={board.id} className="item-row board-row" onClick={() => onSelect(summary)}>
        <span className="board-row-content">
          <span className="item-title">{board.name}</span>
          {board.description && <span className="item-desc muted">{board.description}</span>}
          {board.note && <span className="item-meta muted">{board.note}</span>}
          <BoardStats board={summary} />
          <PolicyBadges board={summary} />
        </span>
        <span className="board-row-actions">
          {summary.favorite && <span className="favorite-btn favorite-btn--active" title={t('board.boardActions.markRead')}>★</span>}
        </span>
      </li>
    )
  }

  function renderFavoriteFolder(folder: FavoriteFolder, depth = 0) {
    const childFolders = foldersByParent[folder.id] ?? []
    const folderBoards = favoritesByFolder[folder.id] ?? []
    const siblings = foldersByParent[folder.parentId ?? ''] ?? []
    const index = siblings.findIndex(item => item.id === folder.id)
    const stats = favoriteScopeStats(folder.id, foldersByParent, favoritesByFolder, boardsById)
    return (
      <div key={folder.id} className="favorite-folder" style={{ marginLeft: `${depth * 0.75}rem` }}>
        <div className="favorite-folder-header">
          <span className="favorite-folder-name">{folder.name}</span>
          <span className="favorite-folder-actions">
            {stats.unreadPosts > 0 && <button className="board-action-btn" onClick={e => markFavoriteScopeRead(folder.id, e)} title={t('board.favoriteActions.markRead')}>✓</button>}
            {stats.hasReadMarker && <button className="board-action-btn" onClick={e => restoreFavoriteScopeRead(folder.id, e)} title={t('board.favoriteActions.restoreReadMarker')}>↶</button>}
            <button className="board-action-btn" onClick={e => createFolder(folder.id, e)} title={t('board.favoriteActions.newNestedFolder')}>＋</button>
            <button className="board-action-btn" onClick={e => renameFolder(folder, e)} title={t('board.favoriteActions.renameFolder')}>✎</button>
            <button className="board-action-btn" disabled={index <= 0} onClick={e => moveFolder(folder, 'up', e)} title={t('board.favoriteActions.moveFolderUp')}>↑</button>
            <button className="board-action-btn" disabled={index < 0 || index >= siblings.length - 1} onClick={e => moveFolder(folder, 'down', e)} title={t('board.favoriteActions.moveFolderDown')}>↓</button>
            <button className="board-action-btn" onClick={e => deleteFolder(folder, e)} title={t('board.favoriteActions.deleteFolder')}>×</button>
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
        <button className="board-action-btn" disabled={index <= 0} onClick={e => moveCategory(category, 'up', e)} title={t('board.favoriteActions.moveCategoryUp')}>↑</button>
        <button className="board-action-btn" disabled={index < 0 || index >= siblings.length - 1} onClick={e => moveCategory(category, 'down', e)} title={t('board.favoriteActions.moveCategoryDown')}>↓</button>
        <button className="board-action-btn" onClick={e => renameCategory(category, e)} title={t('board.favoriteActions.editCategory')} aria-label={t('board.actionRenameCategory', { category: category.name })}>✎</button>
        <button className="board-action-btn" onClick={e => reparentCategory(category, e)} title={t('board.favoriteActions.moveCategory')} aria-label={t('board.actionMoveCategory', { category: category.name })}>⇄</button>
        <select
          className="favorite-folder-select"
          value={category.visibility || 'public'}
          onClick={e => e.stopPropagation()}
          onChange={e => updateCategory(category, { visibility: e.target.value })}
          title={t('board.favoriteActions.directoryVisibility')}
        >
          <option value="public">{t('board.visibility.public')}</option>
          <option value="staff">{t('board.visibility.staff')}</option>
          <option value="hidden">{t('board.visibility.hidden')}</option>
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

  const favoriteStats = favoriteScopeStats('', foldersByParent, favoritesByFolder, boardsById)

  return (
    <div className="board-list">
      {/* ── Tab bar ───────────────────────────────────────────────────── */}
      <div className="board-tabs">
        <button
          className={`board-tab board-tab--unread${activeTab === 'unread' ? ' board-tab--active' : ''}`}
          onClick={() => switchTab('unread')}
        >
          {t('board.tabUnread')}
          {unreadBoards.length > 0 && <span className="board-tab-count">{unreadBoards.length}</span>}
        </button>
        <button
          className={`board-tab${activeTab === 'favorites' ? ' board-tab--active' : ''}`}
          onClick={() => switchTab('favorites')}
        >
          {t('board.tabFavorites')}
        </button>
        <button
          className={`board-tab${activeTab === 'directory' ? ' board-tab--active' : ''}`}
          onClick={() => switchTab('directory')}
        >
          {t('board.tabDirectory')}
        </button>
        <button
          className={`board-tab${activeTab === 'all' ? ' board-tab--active' : ''}`}
          onClick={() => switchTab('all')}
        >
          {t('board.tabAll')}
        </button>
      </div>

      {/* ── Unread tab ────────────────────────────────────────────────── */}
      {activeTab === 'unread' && (
        <div className="board-tab-body">
          {newBoards.length > 0 && (
            <section className="board-section">
              <h3 className="board-section-title">{t('board.newBoardsSection')}</h3>
              <ul className="item-list">{newBoards.map(renderBoard)}</ul>
            </section>
          )}
          {unreadBoards.length === 0
            ? (
              <div className="board-empty">
                <span className="board-empty-icon">✓</span>
                <span>{t('board.allCaughtUp')}</span>
              </div>
            )
            : (
              <ul className="item-list">{unreadBoards.map(renderBoard)}</ul>
            )}
        </div>
      )}

      {/* ── Favorites tab ─────────────────────────────────────────────── */}
      {activeTab === 'favorites' && (
        <div className="board-tab-body">
          <section className="board-section">
            <div className="board-section-heading">
              <h3 className="board-section-title">{t('board.favoritesSection')}</h3>
              <span className="favorite-folder-actions">
                {favoriteStats.unreadPosts > 0 && <button className="board-action-btn" onClick={e => markFavoriteScopeRead('', e)} title={t('board.favoriteActions.markRead')}>✓</button>}
                {favoriteStats.hasReadMarker && <button className="board-action-btn" onClick={e => restoreFavoriteScopeRead('', e)} title={t('board.favoriteActions.restoreReadMarker')}>↶</button>}
                <button className="board-action-btn" onClick={exportFavorites} title={t('board.favoriteActions.export')} aria-label={t('board.favoriteActions.export')}>📤</button>
                <button className="board-action-btn" onClick={importFavorites} title={t('board.favoriteActions.import')} aria-label={t('board.favoriteActions.import')}>📥</button>
                <button className="board-action-btn" onClick={e => createFolder('', e)} title={t('board.favoriteActions.newFolder')}>＋</button>
              </span>
            </div>
            {favoriteTree.folders.length === 0 && favoriteTree.boards.length === 0
              ? (
                <div className="board-empty">
                  <span className="board-empty-icon">☆</span>
                  <span>{t('board.noFavoritesYet')}</span>
                </div>
              )
              : (
                <>
                  {(favoritesByFolder['']?.length ?? 0) > 0 && (
                    <ul className="item-list favorite-folder-list">
                      {(favoritesByFolder[''] ?? []).map(renderFavoriteBoard)}
                    </ul>
                  )}
                  {(foldersByParent[''] ?? []).map(folder => renderFavoriteFolder(folder))}
                </>
              )}
          </section>
          {recommendedVisibleBoards.length > 0 && (
            <section className="board-section">
              <h3 className="board-section-title">{t('board.tabDiscoverSection')}</h3>
              <ul className="item-list">{recommendedVisibleBoards.map(renderRecommendedBoard)}</ul>
            </section>
          )}
        </div>
      )}

      {/* ── Directory tab ─────────────────────────────────────────────── */}
      {activeTab === 'directory' && (
        <div className="board-tab-body">
          <section className="board-section">
            {directoryRoots.length === 0 && uncategorizedBoards.length === 0
              ? <p className="muted">{t('board.noDataRoot')}</p>
              : (
                <>
                  {directoryRoots.map(category => renderCategory(category))}
                  {uncategorizedBoards.length > 0 && (
                    <ul className="item-list" style={{ marginTop: '0.5rem' }}>
                      {uncategorizedBoards.map(renderBoard)}
                    </ul>
                  )}
                </>
              )}
          </section>
        </div>
      )}

      {/* ── All tab ───────────────────────────────────────────────────── */}
      {activeTab === 'all' && (
        <div className="board-tab-body">
          <div className="board-discovery-controls">
            <input
              value={boardQuery}
              onChange={e => setBoardQuery(e.target.value)}
              placeholder={t('board.searchTitle')}
              aria-label={t('board.searchTitle')}
            />
            <select value={boardSort} onChange={e => setBoardSort(e.target.value as BoardSortMode)} aria-label={t('board.sortBoards')}>
              <option value="name">{t('board.nameSort')}</option>
              <option value="new">{t('board.newSort')}</option>
              <option value="online">{t('board.onlineSort')}</option>
              <option value="posts">{t('board.postsSort')}</option>
              <option value="activity">{t('board.activitySort')}</option>
              <option value="unread">{t('board.unreadSort')}</option>
            </select>
          </div>
          {visibleBoards.length === 0
            ? <p className="muted">{t('board.noUnreadBoards')}</p>
            : <ul className="item-list">{visibleBoards.map(renderBoard)}</ul>}
        </div>
      )}
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

function sortRecommendedBoards(boards: RecommendedBoard[]) {
  return boards
    .slice()
    .sort((a, b) => a.position - b.position || b.updatedAt - a.updatedAt || a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
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
  boardsById: Record<string, BoardSummary>,
) {
  const boards = favoritesByFolder[folderId] ?? []
  let unreadPosts = boards.reduce((sum, board) => (boardsById[board.id]?.zapped ? sum : sum + board.unreadPosts), 0)
  let hasReadMarker = boards.some(board => board.readSeq > 0)
  for (const child of foldersByParent[folderId] ?? []) {
    const childStats = favoriteScopeStats(child.id, foldersByParent, favoritesByFolder, boardsById)
    unreadPosts += childStats.unreadPosts
    hasReadMarker = hasReadMarker || childStats.hasReadMarker
  }
  return { unreadPosts, hasReadMarker }
}

function favoriteEntryToSummary(board: FavoriteBoardEntry, existing?: BoardSummary): BoardSummary {
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
    statsExcluded: false,
    zapAllowed: existing?.zapAllowed ?? true,
    zapped: existing?.zapped ?? false,
    moderatorCount: 0,
  }
}

function recommendedBoardToSummary(board: RecommendedBoard, existing?: BoardSummary): BoardSummary {
  return {
    id: board.id,
    name: board.name,
    description: board.description,
    favorite: existing?.favorite ?? false,
    unreadThreads: existing?.unreadThreads ?? 0,
    unreadPosts: existing?.unreadPosts ?? 0,
    threadCount: board.threadCount,
    postCount: board.postCount,
    onlineUsers: board.onlineUsers,
    lastSeq: board.lastSeq,
    readSeq: existing?.readSeq ?? 0,
    createdAt: existing?.createdAt ?? 0,
    newBoard: existing?.newBoard ?? false,
    anonymousAllowed: existing?.anonymousAllowed ?? false,
    readOnly: existing?.readOnly ?? false,
    noReply: existing?.noReply ?? false,
    attachmentsAllowed: existing?.attachmentsAllowed ?? false,
    mailInAllowed: existing?.mailInAllowed ?? false,
    relayEnabled: existing?.relayEnabled ?? false,
    memberReadMode: existing?.memberReadMode ?? false,
    memberPostMode: existing?.memberPostMode ?? false,
    statsExcluded: existing?.statsExcluded ?? false,
    zapAllowed: existing?.zapAllowed ?? true,
    zapped: existing?.zapped ?? false,
    moderatorCount: board.moderatorCount,
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
    board.statsExcluded && 'Hidden from stats',
    !board.zapAllowed && 'No zap',
    board.zapped && 'Zapped',
    board.moderatorCount > 0 && `${board.moderatorCount} mod${board.moderatorCount === 1 ? '' : 's'}`,
  ].filter(Boolean)
  if (badges.length === 0) return null
  return (
    <span className="policy-badge-row">
      {badges.map(badge => <span key={String(badge)} className="policy-badge">{badge}</span>)}
    </span>
  )
}

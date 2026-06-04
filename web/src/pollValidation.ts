interface PollValidation {
  hasPollTag: boolean
  valid: boolean
  message?: string
}

const CLOSE_TAG = '[/poll]'
const OPEN_TAG_PREFIX = '[poll'

function looksLikeValidExpires(raw: string): boolean {
  const value = raw.trim()
  if (!value) return false
  if (/^\d+$/.test(value)) {
    return value !== '0'
  }
  const duration = /^\d+(\.\d+)?[smhd]$/i.test(value)
  if (duration) return true
  return !Number.isNaN(Date.parse(value))
}

function trimBullet(line: string): string {
  return line.replace(/^[-* ]+/, '')
}

export function validatePollMarkup(body: string): PollValidation {
  const openIdx = body.indexOf(OPEN_TAG_PREFIX)
  if (openIdx < 0) {
    return { hasPollTag: false, valid: true }
  }

  const closeBracketIdx = body.indexOf(']', openIdx)
  if (closeBracketIdx < openIdx) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll block has an invalid opening tag.',
    }
  }

  const openTag = body.slice(openIdx, closeBracketIdx + 1)
  const openTagMatch = /^\[poll(?:\s+expires\s*=\s*([^\]]+))?\]$/i.exec(openTag)
  if (!openTagMatch) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll tag is malformed. Use [poll] or [poll expires=<timestamp>].',
    }
  }

  const expires = openTagMatch[1]
  if (expires && !looksLikeValidExpires(expires)) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll closing time is invalid. Use like 2026-06-15T14:30, 2h, 3d, or UNIX ms.',
    }
  }

  const closeIdx = body.indexOf(CLOSE_TAG, closeBracketIdx)
  if (closeIdx < 0) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll block is missing a closing [/poll] tag.',
    }
  }

  const inner = body.slice(closeBracketIdx + 1, closeIdx)
  const lines = inner.split('\n')

  let question = ''
  const options: string[] = []
  for (const rawLine of lines) {
    const line = rawLine.trim()
    if (!line) continue
    if (!question && !line.startsWith('-') && !line.startsWith('*')) {
      question = line
      continue
    }
    const option = trimBullet(line)
    if (option) options.push(option)
  }

  if (!question) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll block is invalid: add a question line before options.',
    }
  }

  if (options.length < 2) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll block is invalid: include at least two options.',
    }
  }

  return { hasPollTag: true, valid: true }
}

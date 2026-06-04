interface PollValidation {
  hasPollTag: boolean
  valid: boolean
  message?: string
}

const OPEN_TAG = '[poll]'
const CLOSE_TAG = '[/poll]'

function trimBullet(line: string): string {
  return line.replace(/^[-* ]+/, '')
}

export function validatePollMarkup(body: string): PollValidation {
  const openIdx = body.indexOf(OPEN_TAG)
  if (openIdx < 0) {
    return { hasPollTag: false, valid: true }
  }

  const closeIdx = body.indexOf(CLOSE_TAG, openIdx + OPEN_TAG.length)
  if (closeIdx < 0) {
    return {
      hasPollTag: true,
      valid: false,
      message: 'Poll block is missing a closing [/poll] tag.',
    }
  }

  const inner = body.slice(openIdx + OPEN_TAG.length, closeIdx)
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


import type { AttachmentPayload } from '../api/types'

interface Props {
  attachments: AttachmentPayload[]
  onChange: (attachments: AttachmentPayload[]) => void
  disabled?: boolean
}

export function AttachmentComposer({ attachments, onChange, disabled = false }: Props) {
  function addAttachment() {
    if (attachments.length >= 8) return
    onChange([...attachments, { filename: '', contentType: '', sizeBytes: 0, url: '' }])
  }

  function updateAttachment(index: number, patch: Partial<AttachmentPayload>) {
    onChange(attachments.map((item, i) => (i === index ? { ...item, ...patch } : item)))
  }

  function removeAttachment(index: number) {
    onChange(attachments.filter((_, i) => i !== index))
  }

  return (
    <div className="attachment-composer">
      <div className="attachment-composer-header">
        <span className="muted">Attachments</span>
        <button type="button" className="link-btn" onClick={addAttachment} disabled={disabled || attachments.length >= 8}>Add file</button>
      </div>
      {attachments.map((item, index) => (
        <div className="attachment-edit-row" key={index}>
          <input
            value={item.filename}
            onChange={e => updateAttachment(index, { filename: e.target.value })}
            placeholder="filename"
            disabled={disabled}
            maxLength={160}
          />
          <input
            value={item.contentType ?? ''}
            onChange={e => updateAttachment(index, { contentType: e.target.value })}
            placeholder="type"
            disabled={disabled}
            maxLength={120}
          />
          <input
            type="number"
            min={0}
            value={item.sizeBytes ?? 0}
            onChange={e => updateAttachment(index, { sizeBytes: Number(e.target.value) || 0 })}
            placeholder="bytes"
            disabled={disabled}
          />
          <input
            value={item.url ?? ''}
            onChange={e => updateAttachment(index, { url: e.target.value })}
            placeholder="url"
            disabled={disabled}
            maxLength={500}
          />
          <button type="button" className="link-btn danger" onClick={() => removeAttachment(index)} disabled={disabled}>Remove</button>
        </div>
      ))}
    </div>
  )
}

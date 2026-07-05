package logmodel

import "context"

type CommandPartitionOffsetSnapshot []CommandPartitionOffset

func NewCommandPartitionOffsetSnapshot(offsets []CommandPartitionOffset, limit int) CommandPartitionOffsetSnapshot {
	return CommandPartitionOffsetSnapshot(offsets).clone(limit)
}

func (s CommandPartitionOffsetSnapshot) ListCommandPartitionOffsets(ctx context.Context, limit int) ([]CommandPartitionOffset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.clone(limit), nil
}

func (s CommandPartitionOffsetSnapshot) clone(limit int) CommandPartitionOffsetSnapshot {
	if limit > 0 && len(s) > limit {
		s = s[:limit]
	}
	out := make(CommandPartitionOffsetSnapshot, 0, len(s))
	for _, offset := range s {
		out = append(out, offset.Normalize())
	}
	return out
}

package logmodel

import "sort"

const (
	PartitionGlobal = "global"
	PartitionBoard  = "board"
	PartitionThread = "thread"
	PartitionPost   = "post"
	PartitionPoll   = "poll"
	PartitionUser   = "user"
	PartitionMail   = "mail"
	PartitionChat   = "chat"
	PartitionReview = "review"
)

// Partition is the write-ordering key for command and event logs.
type Partition struct {
	Kind string
	Key  string
}

func (p Partition) Normalize() Partition {
	if p.Kind == "" {
		p.Kind = PartitionGlobal
	}
	if p.Key == "" {
		p.Key = PartitionGlobal
	}
	return p
}

func (p Partition) Less(other Partition) bool {
	p = p.Normalize()
	other = other.Normalize()
	if p.Kind == other.Kind {
		return p.Key < other.Key
	}
	return p.Kind < other.Kind
}

func SortPartitions(partitions []Partition) {
	sort.Slice(partitions, func(i, j int) bool {
		return partitions[i].Less(partitions[j])
	})
}

type CommandPartitionOffset struct {
	Partition       Partition
	TailOffset      int64
	CommittedOffset int64
}

func (offset CommandPartitionOffset) Normalize() CommandPartitionOffset {
	offset.Partition = offset.Partition.Normalize()
	if offset.TailOffset < 0 {
		offset.TailOffset = 0
	}
	if offset.CommittedOffset < 0 {
		offset.CommittedOffset = 0
	}
	if offset.CommittedOffset > offset.TailOffset {
		offset.CommittedOffset = offset.TailOffset
	}
	return offset
}

// Lag returns normalized tail minus committed offset, never below zero.
func (offset CommandPartitionOffset) Lag() int64 {
	offset = offset.Normalize()
	return offset.TailOffset - offset.CommittedOffset
}

// CommandPartitionsByTailOffset returns the unique partitions in offsets
// ordered by descending tail offset, then partition kind and key.
func CommandPartitionsByTailOffset(offsets []CommandPartitionOffset, limit int) []Partition {
	tails := map[Partition]int64{}
	for _, offset := range offsets {
		partition := offset.Partition.Normalize()
		if _, ok := tails[partition]; !ok || offset.TailOffset > tails[partition] {
			tails[partition] = offset.TailOffset
		}
	}
	partitions := make([]Partition, 0, len(tails))
	for partition := range tails {
		partitions = append(partitions, partition)
	}
	sort.Slice(partitions, func(a, b int) bool {
		if tails[partitions[a]] == tails[partitions[b]] {
			return partitions[a].Less(partitions[b])
		}
		return tails[partitions[a]] > tails[partitions[b]]
	})
	if limit > 0 && len(partitions) > limit {
		partitions = partitions[:limit]
	}
	return partitions
}

// SortCommandPartitionOffsetsByLag orders offsets by descending uncommitted
// lag, descending tail offset, then partition kind and key.
func SortCommandPartitionOffsetsByLag(offsets []CommandPartitionOffset) {
	sort.SliceStable(offsets, func(a, b int) bool {
		la := offsets[a].Lag()
		lb := offsets[b].Lag()
		if la == lb {
			if offsets[a].TailOffset == offsets[b].TailOffset {
				return offsets[a].Partition.Less(offsets[b].Partition)
			}
			return offsets[a].TailOffset > offsets[b].TailOffset
		}
		return la > lb
	})
}

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

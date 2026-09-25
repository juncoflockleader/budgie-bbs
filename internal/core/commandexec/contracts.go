package commandexec

import (
	"github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// EventPublisher is the publish-only event bus surface command execution needs.
type EventPublisher interface {
	Publish(evt *proto.Event)
}

// Reply is the result returned by command execution.
type Reply struct {
	Result *proto.AckResult
	Err    *proto.ErrorDetail
}

type Partition = logmodel.Partition

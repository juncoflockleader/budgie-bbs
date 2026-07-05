package logmodel

// BrokerCommandLogMessage is a physical broker command-log message plus its
// logical command offset. StreamSeq is optional broker metadata used only for
// scalar diagnostics.
type BrokerCommandLogMessage struct {
	Partition Partition
	Offset    int64
	StreamSeq int64
	Data      []byte
}

func CloneBrokerCommandLogMessage(msg BrokerCommandLogMessage) BrokerCommandLogMessage {
	msg.Partition = msg.Partition.Normalize()
	msg.Data = append([]byte(nil), msg.Data...)
	return msg
}

// BrokerEventLogMessage is a physical broker event-log message plus its logical
// event offset. StreamSeq is optional broker metadata used only for scalar
// diagnostics.
type BrokerEventLogMessage struct {
	Partition Partition
	Offset    int64
	StreamSeq int64
	Data      []byte
}

func CloneBrokerEventLogMessage(msg BrokerEventLogMessage) BrokerEventLogMessage {
	msg.Partition = msg.Partition.Normalize()
	msg.Data = append([]byte(nil), msg.Data...)
	return msg
}

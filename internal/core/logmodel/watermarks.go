package logmodel

import "strings"

const DefaultEventStoreProjectionSource = "event-store"

func NormalizeEventStoreProjectionSource(source string) string {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return DefaultEventStoreProjectionSource
	}
	return source
}

func EventStoreProjectionWatermarkName(source string, partition Partition) string {
	partition = partition.Normalize()
	return "event-store-projection:" +
		EncodeSubjectToken(NormalizeEventStoreProjectionSource(source)) + ":" +
		EncodeSubjectToken(partition.Kind) + ":" +
		EncodeSubjectToken(partition.Key)
}

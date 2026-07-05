package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"

func SyntheticCommandLogCID(partition LogPartition, offset int64) string {
	return logmodel.SyntheticCommandLogCID(partition, offset)
}

func EffectiveCommandLogCID(record CommandLogRecord) (string, error) {
	return logmodel.EffectiveCommandLogCID(record)
}

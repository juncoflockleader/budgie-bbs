package core

import "github.com/juncoflockleader/budgie-bbs/internal/core/logmodel"

type commandReceiptKey = logmodel.CommandReceiptKey

func newCommandReceiptKey(partition LogPartition, actorID, cid string) (commandReceiptKey, bool) {
	return logmodel.NewCommandReceiptKey(partition, actorID, cid)
}

func SyntheticCommandLogCID(partition LogPartition, offset int64) string {
	return logmodel.SyntheticCommandLogCID(partition, offset)
}

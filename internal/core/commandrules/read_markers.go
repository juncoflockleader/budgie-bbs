package commandrules

import "github.com/juncoflockleader/budgie-bbs/internal/proto"

func ApplyReadMarker(targetID string, require func() *proto.ErrorDetail, update func() error) (*proto.AckResult, *proto.ErrorDetail) {
	if errDetail := require(); errDetail != nil {
		return nil, errDetail
	}
	if err := update(); err != nil {
		return nil, internalErr(err)
	}
	return &proto.AckResult{ID: targetID}, nil
}

package vm

import (
	"gitlab.com/akita/util/ca"
)

type NBWalkerBlock struct {
	PID            ca.PID
	Address        uint64
	Level          int
	PromotionLevel int

	// For CAM, do not occupy space.
	PPNWithOffset uint64
	PPN           uint64

	// For debugging.
	MsgID string
}

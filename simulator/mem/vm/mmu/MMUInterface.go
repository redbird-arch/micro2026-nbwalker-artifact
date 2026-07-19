package mmu

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/util/tracing"
)

type MMU interface {
	tracing.NamedHookable

	GetNumActiveWalkers() int
	ToTopPort() akita.Port
	ToTranslationPort() akita.Port
	ToCachePort() akita.Port
	ToPageWalkCachePort() akita.Port
	SetLowModuleFinder(lmf cache.LowModuleFinder)
	CanAccept() bool
}

type Transaction interface {
	TaskID() string
	Meta() *akita.MsgMeta

	GetPPN() uint64
	GetMemReq() *mem.ReadReq
}

func ExtractMPID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[2][3:5])
	if err != nil {
		panic(fmt.Sprintf("failed to extract MP ID from port name %s: %v", portName, err))
	}

	return id
}

func Uint64ToBytes(data uint64) []byte {
	bytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(bytes, data)
	return bytes
}

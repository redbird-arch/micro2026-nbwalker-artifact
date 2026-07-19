package l1cache

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/util/tracing"
)

type Cache interface {
	tracing.NamedHookable
	akita.Component

	SetLowModuleFinder(lmf cache.LowModuleFinder)
	GetTopPort() akita.Port
	GetBottomPort() akita.Port
	GetControlPort() akita.Port
	GetWalkerPort() akita.Port
}

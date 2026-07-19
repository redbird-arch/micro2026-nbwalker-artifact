package NBWalker

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/util"
)

// A Cache is a customized L1 cache the for R9nano GPUs.
type Cache struct {
	*akita.TickingComponent

	TopPort     akita.Port
	BottomPort  akita.Port
	ControlPort akita.Port
	WalkerPort  akita.Port
	PageWalker  akita.Port

	numReqPerCycle   int
	log2BlockSize    uint64
	storage          *mem.Storage
	directory        cache.Directory
	mshr             cache.MSHR
	bankLatency      int
	wayAssociativity int
	lowModuleFinder  cache.LowModuleFinder

	dirBuf       util.Buffer
	walkerDirBuf util.Buffer
	bankBufs     []util.Buffer

	coalesceStage    *coalescer
	walkerStage      *walkerStage
	directoryStage   *directory
	bankStages       []*bankStage
	parseBottomStage *bottomParser
	respondStage     *respondStage
	controlStage     *controlStage

	transactions             []*transaction
	postCoalesceTransactions []*transaction

	isPaused bool

	isInstCache bool

	monitorStats *monitor.CaPWQMonitorStats

	extendBits uint64
	offsetMask uint64

	numReservedPTWEntry int
}

func (c *Cache) SentCommand(info interface{}) {
	c.numReservedPTWEntry = info.(int)
}

func (c *Cache) InitMonitorStats() {
	c.monitorStats = &monitor.CaPWQMonitorStats{}
}

func (c *Cache) ClearMonitorStats() {
	c.monitorStats.Clear()
}

func (c *Cache) GetMonitorStats() interface{} {
	return c.monitorStats
}

// SetLowModuleFinder sets the finder that tells which remote port can serve
// the data on a certain address.
func (c *Cache) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	c.lowModuleFinder = lmf
}

// Tick update the state of the cache
func (c *Cache) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	if !c.isPaused {
		madeProgress = c.runPipeline(now) || madeProgress
	}

	madeProgress = c.controlStage.Tick(now) || madeProgress

	return madeProgress
}

func (c *Cache) runPipeline(now akita.VTimeInSec) bool {
	madeProgress := false
	madeProgress = c.tickRespondStage(now) || madeProgress
	madeProgress = c.tickParseBottomStage(now) || madeProgress
	madeProgress = c.tickBankStage(now) || madeProgress
	madeProgress = c.tickDirectoryStage(now) || madeProgress
	madeProgress = c.tickCoalesceState(now) || madeProgress
	madeProgress = c.tickWalkerStage(now) || madeProgress
	return madeProgress
}

func (c *Cache) tickRespondStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.respondStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickParseBottomStage(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.parseBottomStage.Tick(now) || madeProgress
	}

	return madeProgress
}

func (c *Cache) tickBankStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for _, bs := range c.bankStages {
		madeProgress = bs.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickDirectoryStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.directoryStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickWalkerStage(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.walkerStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickCoalesceState(now akita.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.coalesceStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) GetTopPort() akita.Port {
	return c.TopPort
}

func (c *Cache) GetBottomPort() akita.Port {
	return c.BottomPort
}

func (c *Cache) GetControlPort() akita.Port {
	return c.ControlPort
}

func (c *Cache) GetName() string {
	return c.Name()
}

func (c *Cache) GetWalkerPort() akita.Port {
	return c.WalkerPort
}

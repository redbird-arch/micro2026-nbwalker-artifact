package NBWalkerMMU

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A NBWalkerMMUBuilder can build MMU component
type NBWalkerMMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	maxNumReqInFlight        int
	log2CacheLineSize        uint64
	pageWalkCacheSize        uint64
}

// MakeBuilder creates a new builder
func MakeNBWalkerMMUBuilder() NBWalkerMMUBuilder {
	return NBWalkerMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8, //16,
		log2CacheLineSize: 6,
		pageWalkCacheSize: 512, //256, //bytes
	}
}

// WithEngine sets the engine to be used with the MMU
func (b NBWalkerMMUBuilder) WithEngine(engine akita.Engine) NBWalkerMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b NBWalkerMMUBuilder) WithFreq(freq akita.Freq) NBWalkerMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b NBWalkerMMUBuilder) WithLog2PageSize(log2PageSize uint64) NBWalkerMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b NBWalkerMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) NBWalkerMMUBuilder {
	b.pageTable = pageTable
	return b
}

// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b NBWalkerMMUBuilder) WithMaxNumReqInFlight(n int) NBWalkerMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

// WithPageWalkCacheSize sets the size of the page walk cache.
func (b NBWalkerMMUBuilder) WithPageWalkCacheSize(size uint64) NBWalkerMMUBuilder {
	b.pageWalkCacheSize = size
	return b
}

// WithLog2CacheLineSize sets the cache line size of the cache connected to
// the MMU.
func (b NBWalkerMMUBuilder) WithLog2CacheLineSize(
	log2CacheLineSize uint64,
) NBWalkerMMUBuilder {
	b.log2CacheLineSize = log2CacheLineSize
	return b
}

// Build returns a newly created MMU component
func (b NBWalkerMMUBuilder) Build(name string) mmu.MMU {
	mmu := new(NBWalkerMMU)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmu.ToCache = akita.NewLimitNumMsgPort(mmu, 16, name+".ToCache")
	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 16, name+".TranslationPort")
	mmu.translationSender = akitaext.NewBufferedSender(mmu.TranslationPort, util.NewBuffer(16))

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(16))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}

	mmu.pageWalkers = make([]*CaPWQPageWalker, 0, b.maxNumReqInFlight)
	for i := 0; i < b.maxNumReqInFlight; i++ {
		walker := newCaPWQPageWalker(mmu, i)

		mmu.pageWalkers = append(mmu.pageWalkers, walker)
	}

	mmu.pageWalkReqQueue = make([]*device.TranslationReq, 0)
	mmu.pageWalkRspQueue = make([]*transactionImpl, 0)

	totalEntry := b.maxNumReqInFlight * 8

	mmu.walkReqQueueCapacity = totalEntry / 2
	mmu.walkRspQueueCapacity = totalEntry / 4

	mmu.ToPageWalkCache = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToPageWalkCache")

	mmu.log2CacheLineSize = b.log2CacheLineSize

	return mmu
}

package infinite

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A MMUBuilder can build MMU component
type MMUBuilder struct {
	engine       akita.Engine
	freq         akita.Freq
	log2PageSize uint64
	pageTable    *device.PageTableImpl
}

// MakeBuilder creates a new builder
func MakeMMUBuilder() MMUBuilder {
	return MMUBuilder{
		freq:         1 * akita.GHz,
		log2PageSize: 12,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b MMUBuilder) WithEngine(engine akita.Engine) MMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b MMUBuilder) WithFreq(freq akita.Freq) MMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b MMUBuilder) WithLog2PageSize(log2PageSize uint64) MMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b MMUBuilder) WithPageTable(pageTable *device.PageTableImpl) MMUBuilder {
	b.pageTable = pageTable
	return b
}

// Build returns a newly created MMU component
func (b MMUBuilder) Build(name string) mmu.MMU {
	mmu := new(MMUImpl)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")

	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 16, name+".TranslationPort")

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(16))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}

	mmu.pageWalkers = make([]*PageWalkerImpl, 0)

	mmu.ToPageWalkCache = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToPageWalkCache")

	return mmu
}

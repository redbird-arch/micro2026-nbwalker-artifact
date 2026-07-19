package neighboraware

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A NeighBorMMUBuilder can build MMU component
type NeighBorMMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	maxNumReqInFlight        int
}

// MakeNeighBorMMUBuilder creates a new builder
func MakeNeighBorMMUBuilder() NeighBorMMUBuilder {
	return NeighBorMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 8, //16,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b NeighBorMMUBuilder) WithEngine(engine akita.Engine) NeighBorMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b NeighBorMMUBuilder) WithFreq(freq akita.Freq) NeighBorMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b NeighBorMMUBuilder) WithLog2PageSize(log2PageSize uint64) NeighBorMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b NeighBorMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) NeighBorMMUBuilder {
	b.pageTable = pageTable
	return b
}

// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b NeighBorMMUBuilder) WithMaxNumReqInFlight(n int) NeighBorMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

// Build returns a newly created MMU component
func (b NeighBorMMUBuilder) Build(name string) mmu.MMU {
	mmu := new(NeighBorMMU)
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

	mmu.queueCapacity = b.maxNumReqInFlight * 8
	mmu.pageWalkers = make([]PageWalkerImpl, 0, b.maxNumReqInFlight)
	for i := 0; i < b.maxNumReqInFlight; i++ {
		walker := PageWalkerImpl{
			inflightTrans: nil,
		}

		mmu.pageWalkers = append(mmu.pageWalkers, walker)
	}
	mmu.nextPointer = 0

	mmu.ToPageWalkCache = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToPageWalkCache")

	return mmu
}

package mpw

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
)

// A MPWMMUBuilder can build MMU component
type MPWMMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	maxNumReqInFlight        int
}

// MakeMPWMMUBuilder creates a new builder
func MakeMPWMMUBuilder() MPWMMUBuilder {
	return MPWMMUBuilder{
		freq:              1 * akita.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 16, //16,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b MPWMMUBuilder) WithEngine(engine akita.Engine) MPWMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b MPWMMUBuilder) WithFreq(freq akita.Freq) MPWMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b MPWMMUBuilder) WithLog2PageSize(log2PageSize uint64) MPWMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b MPWMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) MPWMMUBuilder {
	b.pageTable = pageTable
	return b
}

// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b MPWMMUBuilder) WithMaxNumReqInFlight(n int) MPWMMUBuilder {
	b.maxNumReqInFlight = n
	return b
}

// Build returns a newly created MMU component
func (b MPWMMUBuilder) Build(name string) mmu.MMU {
	mmu := new(MPWMMU)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")

	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 16, name+".TranslationPort")

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(16))
	mmu.translationSender = akitaext.NewBufferedSender(mmu.TranslationPort, util.NewBuffer(16))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}

	mmu.queueCapacity = 8
	mmu.pageWalkers = make([]*MPWPageWalker, 0, b.maxNumReqInFlight)
	for i := 0; i < b.maxNumReqInFlight; i++ {
		walker := MPWPageWalker{
			queue: make([]*transactionImpl, 0),
			status: &MPWWalkerStatus{
				state:         newTransaction,
				requestVector: make(map[int]struct{}),
			},
		}

		mmu.pageWalkers = append(mmu.pageWalkers, &walker)
	}
	mmu.nextPointer = 0

	mmu.ToPageWalkCache = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToPageWalkCache")

	return mmu
}

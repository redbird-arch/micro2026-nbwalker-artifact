package mmu

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/pipelining"
)

// A IdealMMUBuilder can build MMU component
type IdealMMUBuilder struct {
	engine                   akita.Engine
	freq                     akita.Freq
	log2PageSize             uint64
	pageTable                *device.PageTableImpl
	migrationServiceProvider akita.Port
	latency                  int
	maxActiveTransactions    uint64
	//	lowAddr                  uint64
	//	totMem                   uint64
	//	bankSize                 uint64
	//	numMemoryBanksPerChiplet uint64
}

// MakeBuilder creates a new builder
func MakeIdealMMUBuilder() IdealMMUBuilder {
	return IdealMMUBuilder{
		freq:         1 * akita.GHz,
		log2PageSize: 12,
		latency:      200,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b IdealMMUBuilder) WithEngine(engine akita.Engine) IdealMMUBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b IdealMMUBuilder) WithFreq(freq akita.Freq) IdealMMUBuilder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b IdealMMUBuilder) WithLog2PageSize(log2PageSize uint64) IdealMMUBuilder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b IdealMMUBuilder) WithPageTable(pageTable *device.PageTableImpl) IdealMMUBuilder {
	b.pageTable = pageTable
	return b
}

/*
// WithMigrationServiceProvider sets the destination port that can perform
// page migration.
func (b IdealMMUBuilder) WithMigrationServiceProvider(p akita.Port) IdealMMUBuilder {
	b.migrationServiceProvider = p
	return b
}
*/
// WithLatency sets the latency of the MMU in cycles.
func (b IdealMMUBuilder) WithLatency(n int) IdealMMUBuilder {
	b.latency = n
	return b
}

func (b IdealMMUBuilder) WithMaxActiveTransactions(n uint64) IdealMMUBuilder {
	b.maxActiveTransactions = n
	return b
}

/*
// WithPageWalkingLatency sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithPageWalkingLatency(n int) IdealMMUBuilder {
	b.pageWalkingLatency = n
	return b
}
*/

/*
// WithLowAddr sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithLowAddr(la uint64) IdealMMUBuilder {
	b.lowAddr = la
	return b
}

// WithTotMem sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithTotMem(ha uint64) IdealMMUBuilder {
	b.totMem = ha
	return b
}

// WithBankSize sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithBankSize(n uint64) IdealMMUBuilder {
	b.bankSize = n
	return b
}

// WithNumMemoryBankPerChiplet sets the number of cycles required for walking a page
// table.
func (b IdealMMUBuilder) WithNumMemoryBankPerChiplet(n uint64) IdealMMUBuilder {
	b.numMemoryBanksPerChiplet = n
	return b
}
*/
// Build returns a newly created MMU component
func (b IdealMMUBuilder) Build(name string) MMU {
	mmu := new(IdealMMU)
	mmu.TickingComponent = *akita.NewTickingComponent(
		name, b.engine, b.freq, mmu)
	//mmu.migrationQueueSize = 4096

	mmu.ToTop = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmu.ControlPort = akita.NewLimitNumMsgPort(mmu, 1, name+".ControlPort")

	//mmu.MigrationPort = akita.NewLimitNumMsgPort(mmu, 1, name+".MigrationPort")
	//might want to change capacity later
	mmu.TranslationPort = akita.NewLimitNumMsgPort(mmu, 4096, name+".TranslationPort")
	//mmu.MigrationServiceProvider = b.migrationServiceProvider

	mmu.lookupBuffer = util.NewBuffer(4096)
	pipelineBuilder := pipelining.MakeBuilder().WithPipelineWidth(1024).WithNumStage(b.latency).WithCyclePerStage(1).WithPostPipelineBuffer(mmu.lookupBuffer)
	mmu.pipeline = pipelineBuilder.Build(mmu.Name() + "_pipeline")

	mmu.maxActiveTransactions = b.maxActiveTransactions

	mmu.topSender = akitaext.NewBufferedSender(mmu.ToTop, util.NewBuffer(4096))
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		panic("no page table!")
	}

	mmu.ToPageWalkCache = akita.NewLimitNumMsgPort(mmu, 4096, name+".ToPageWalkCache")

	mmu.sendStateInfo = false
	return mmu
}

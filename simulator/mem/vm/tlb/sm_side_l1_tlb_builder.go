package tlb

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

// A SMSideL1TLBBuilder can build TLBs
type SMSideL1TLBBuilder struct {
	engine         akita.Engine
	freq           akita.Freq
	numReqPerCycle int
	numSets        int
	numWays        int
	pageSize       uint64
	lowModule      akita.Port
	latency        int
	numMSHREntry   int
}

// MakeBuilder returns a SMSideL1TLBBuilder
func MakeSMSideL1TLBBuilder() SMSideL1TLBBuilder {
	return SMSideL1TLBBuilder{
		freq:           1 * akita.GHz,
		numReqPerCycle: 4,
		numSets:        1,
		numWays:        32,
		pageSize:       4096,
		latency:        1,
		numMSHREntry:   4,
	}
}

// WithEngine sets the engine that the TLBs to use
func (b SMSideL1TLBBuilder) WithEngine(engine akita.Engine) SMSideL1TLBBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the freq the TLBs use
func (b SMSideL1TLBBuilder) WithFreq(freq akita.Freq) SMSideL1TLBBuilder {
	b.freq = freq
	return b
}

// WithNumSets sets the number of sets in a L1TLB. Use 1 for fully associated
// TLBs.
func (b SMSideL1TLBBuilder) WithNumSets(n int) SMSideL1TLBBuilder {
	b.numSets = n
	return b
}

// WithNumWays sets the number of ways in a L1TLB. Set this field to the number
// of L1TLB entries for all the functions.
func (b SMSideL1TLBBuilder) WithNumWays(n int) SMSideL1TLBBuilder {
	b.numWays = n
	return b
}

// WithPageSize sets the page size that the L1TLB works with.
func (b SMSideL1TLBBuilder) WithPageSize(n uint64) SMSideL1TLBBuilder {
	b.pageSize = n
	return b
}

// WithNumReqPerCycle sets the number of requests per cycle can be processed by
// a L1TLB
func (b SMSideL1TLBBuilder) WithNumReqPerCycle(n int) SMSideL1TLBBuilder {
	b.numReqPerCycle = n
	return b
}

// WithLatency sets the number of mshr entry
func (b SMSideL1TLBBuilder) WithLatency(latency int) SMSideL1TLBBuilder {
	b.latency = latency
	return b
}

// WithLowModule sets the port that can provide the address translation in case
// of tlb miss.
func (b SMSideL1TLBBuilder) WithLowModule(lowModule akita.Port) SMSideL1TLBBuilder {
	b.lowModule = lowModule
	return b
}

// WithNumMSHREntry sets the number of mshr entry
func (b SMSideL1TLBBuilder) WithNumMSHREntry(num int) SMSideL1TLBBuilder {
	b.numMSHREntry = num
	return b
}

// Build creates a new L1TLB
func (b SMSideL1TLBBuilder) Build(name string) *SMSideL1TLB {
	tlb := &SMSideL1TLB{}
	tlb.TickingComponent =
		akita.NewTickingComponent(name, b.engine, b.freq, tlb)

	tlb.numSets = b.numSets
	tlb.numWays = b.numWays
	tlb.numReqPerCycle = b.numReqPerCycle
	tlb.pageSize = b.pageSize
	tlb.latency = b.latency
	tlb.LowModule = b.lowModule

	tlb.TopPort = akita.NewLimitNumMsgPort(tlb, b.numReqPerCycle,
		name+".TopPort")
	tlb.RemotePort = akita.NewLimitNumMsgPort(tlb, 4*b.numReqPerCycle,
		name+".RemotePort")
	tlb.LocalPort = akita.NewLimitNumMsgPort(tlb, 4*b.numReqPerCycle,
		name+".LocalPort")
	tlb.ControlPort = akita.NewLimitNumMsgPort(tlb, 1,
		name+".ControlPort")
	tlb.mshr = newMSHR(b.numMSHREntry)

	tlb.lookupBuffer = util.NewBuffer(2 * tlb.numReqPerCycle)
	pipelineBuilder := pipelining.MakeBuilder().WithPipelineWidth(tlb.numReqPerCycle).WithNumStage(tlb.latency).WithCyclePerStage(1).WithPostPipelineBuffer(tlb.lookupBuffer)
	tlb.pipeline = pipelineBuilder.Build(tlb.Name() + "_pipeline")

	tlb.reset()

	return tlb
}

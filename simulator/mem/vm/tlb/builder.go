package tlb

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

// A Builder can build TLBs
type Builder struct {
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

// MakeBuilder returns a Builder
func MakeBuilder() Builder {
	return Builder{
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
func (b Builder) WithEngine(engine akita.Engine) Builder {
	b.engine = engine
	return b
}

// WithFreq sets the freq the TLBs use
func (b Builder) WithFreq(freq akita.Freq) Builder {
	b.freq = freq
	return b
}

// WithNumSets sets the number of sets in a TLB. Use 1 for fully associated
// TLBs.
func (b Builder) WithNumSets(n int) Builder {
	b.numSets = n
	return b
}

// WithNumWays sets the number of ways in a TLB. Set this field to the number
// of TLB entries for all the functions.
func (b Builder) WithNumWays(n int) Builder {
	b.numWays = n
	return b
}

// WithPageSize sets the page size that the TLB works with.
func (b Builder) WithPageSize(n uint64) Builder {
	b.pageSize = n
	return b
}

// WithNumReqPerCycle sets the number of requests per cycle can be processed by
// a TLB
func (b Builder) WithNumReqPerCycle(n int) Builder {
	b.numReqPerCycle = n
	return b
}

// WithLatency sets the number of mshr entry
func (b Builder) WithLatency(latency int) Builder {
	b.latency = latency
	return b
}

// WithLowModule sets the port that can provide the address translation in case
// of tlb miss.
func (b Builder) WithLowModule(lowModule akita.Port) Builder {
	b.lowModule = lowModule
	return b
}

// WithNumMSHREntry sets the number of mshr entry
func (b Builder) WithNumMSHREntry(num int) Builder {
	b.numMSHREntry = num
	return b
}

// Build creates a new TLB
func (b Builder) Build(name string) *TLBImpl {
	tlb := &TLBImpl{}
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
	tlb.BottomPort = akita.NewLimitNumMsgPort(tlb, 4*b.numReqPerCycle,
		name+".BottomPort")
	tlb.ControlPort = akita.NewLimitNumMsgPort(tlb, 1,
		name+".ControlPort")
	tlb.mshr = newMSHR(b.numMSHREntry)

	tlb.lookupBuffer = util.NewBuffer(2 * tlb.numReqPerCycle)
	pipelineBuilder := pipelining.MakeBuilder().WithPipelineWidth(tlb.numReqPerCycle).WithNumStage(tlb.latency).WithCyclePerStage(1).WithPostPipelineBuffer(tlb.lookupBuffer)
	tlb.pipeline = pipelineBuilder.Build(tlb.Name() + "_pipeline")

	tlb.reset()

	return tlb
}

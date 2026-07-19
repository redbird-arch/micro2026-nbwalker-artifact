package tlb

import (
	"math"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

// A SMSideTLBBuilder can build LatTLBs
type SMSideTLBBuilder struct {
	engine               akita.Engine
	freq                 akita.Freq
	numReqPerCycle       int
	numSets              int
	numWays              int
	pageSize             uint64
	log2PageSize         uint64
	lowModule            akita.Port
	accessLatency        int
	numMSHREntry         int
	mask                 uint64
	useCoalescingTLBPort bool
}

// MakeLatTLBBuilder returns a SMSideTLBBuilder
func MakeSMSideTLBBuilder() SMSideTLBBuilder {
	return SMSideTLBBuilder{
		freq:           1 * akita.GHz,
		numReqPerCycle: 4,
		numSets:        1,
		numWays:        32,
		pageSize:       4096,
		numMSHREntry:   4,
		accessLatency:  40,
		mask:           uint64(127) << 12,
	}
}

// WithEngine sets the engine that the LatTLBs to use
func (b SMSideTLBBuilder) WithEngine(engine akita.Engine) SMSideTLBBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the freq the LatTLBs use
func (b SMSideTLBBuilder) WithFreq(freq akita.Freq) SMSideTLBBuilder {
	b.freq = freq
	return b
}

// WithNumSets sets the number of sets in a LatTLB. Use 1 for fully associated
// LatTLBs.
func (b SMSideTLBBuilder) WithNumSets(n int) SMSideTLBBuilder {
	b.numSets = n
	return b
}

// WithNumWays sets the number of ways in a LatTLB. Set this field to the number
// of LatTLB entries for all the functions.
func (b SMSideTLBBuilder) WithNumWays(n int) SMSideTLBBuilder {
	b.numWays = n
	return b
}

// WithPageSize sets the page size that the LatTLB works with.
func (b SMSideTLBBuilder) WithPageSize(n uint64) SMSideTLBBuilder {
	b.pageSize = n
	return b
}

// WithNumReqPerCycle sets the number of requests per cycle can be processed by
// a LatTLB
func (b SMSideTLBBuilder) WithNumReqPerCycle(n int) SMSideTLBBuilder {
	b.numReqPerCycle = n
	return b
}

// WithLowModule sets the port that can provide the address translation in case
// of tlb miss.
func (b SMSideTLBBuilder) WithLowModule(lowModule akita.Port) SMSideTLBBuilder {
	b.lowModule = lowModule
	return b
}

// WithNumMSHREntry sets the number of mshr entry
func (b SMSideTLBBuilder) WithNumMSHREntry(num int) SMSideTLBBuilder {
	b.numMSHREntry = num
	return b
}

// WithLatency sets the number of mshr entry
func (b SMSideTLBBuilder) WithAccessLatency(latency int) SMSideTLBBuilder {
	b.accessLatency = latency
	return b
}

// WithIndexingMask sets the bits to use for indexing
func (b SMSideTLBBuilder) WithIndexingMask(mask uint64) SMSideTLBBuilder {
	b.mask = mask
	return b
}

// WithIndexingMask sets the bits to use for indexing
func (b SMSideTLBBuilder) WithLog2PageSize(log2PageSize uint64) SMSideTLBBuilder {
	b.log2PageSize = log2PageSize
	return b
}

func (b SMSideTLBBuilder) UseCoalescingTLBPort() SMSideTLBBuilder {
	b.useCoalescingTLBPort = true
	return b
}

// Build creates a new SMSideTLB
func (b SMSideTLBBuilder) Build(name string) TLB {
	tlb := &SMSideTLB{}
	tlb.TickingComponent =
		akita.NewTickingComponent(name, b.engine, b.freq, tlb)

	tlb.numSets = b.numSets

	tlb.log2NumSets = uint64(math.Log2(float64(tlb.numSets)))
	tlb.setMask = uint64(tlb.numSets - 1)

	tlb.numWays = b.numWays
	tlb.numReqPerCycle = b.numReqPerCycle
	tlb.pageSize = b.pageSize
	tlb.accessLatency = b.accessLatency
	tlb.LowModule = b.lowModule
	tlb.indexingMask = b.mask
	if b.log2PageSize == 0 {
		panic("need to set page size in tlb!")
	}
	tlb.log2PageSize = b.log2PageSize
	if b.useCoalescingTLBPort {
		panic("Do not support coalescing tlb port in sm side tlb")
	} else {
		// tlb.TopPort = akita.NewLimitNumMsgPort(tlb, 16*b.numReqPerCycle,
		// name+".TopPort")
		tlb.LocalTopPort = akita.NewLimitNumMsgPort(tlb, 32,
			name+".LocalTopPort")
		tlb.RemoteTopPort = akita.NewLimitNumMsgPort(tlb, 480,
			name+".RemoteTopPort")
	}
	tlb.BottomPort = akita.NewLimitNumMsgPort(tlb, b.numReqPerCycle,
		name+".BottomPort")
	tlb.ControlPort = akita.NewLimitNumMsgPort(tlb, 1,
		name+".ControlPort")
	tlb.mshr = newMSHR(b.numMSHREntry)
	tlb.lookupBuffer = util.NewBuffer(2 * tlb.numReqPerCycle)
	pipelineBuilder := pipelining.MakeBuilder().WithPipelineWidth(tlb.numReqPerCycle).WithNumStage(tlb.accessLatency).WithCyclePerStage(1).WithPostPipelineBuffer(tlb.lookupBuffer)
	tlb.pipeline = pipelineBuilder.Build(tlb.Name() + "_access_pipeline")

	tlb.reset()

	return tlb
}

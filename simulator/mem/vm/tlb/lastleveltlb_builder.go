package tlb

import (
	"fmt"
	"math"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/tlb/internal"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
)

// A LastLevelTLBBuilder can build LatTLBs
type LastLevelTLBBuilder struct {
	engine               akita.Engine
	freq                 akita.Freq
	numReqPerCycle       int
	numSets              int
	numWays              int
	pageSize             uint64
	log2PageSize         uint64
	latency              int
	numMSHREntry         int
	useCoalescingTLBPort bool
	pageWalkCacheSize    uint64
	dispatchPolicy       string
}

// MakeLastLevelTLBBuilder returns a LastLevelTLBBuilder
func MakeLastLevelTLBBuilder() LastLevelTLBBuilder {
	return LastLevelTLBBuilder{
		freq:              1 * akita.GHz,
		numReqPerCycle:    4,
		numSets:           1,
		numWays:           32,
		pageSize:          4096,
		numMSHREntry:      4,
		latency:           1,
		pageWalkCacheSize: 512, //512 bytes
		dispatchPolicy:    "roundrobin",
	}
}

// WithEngine sets the engine that the LatTLBs to use
func (b LastLevelTLBBuilder) WithEngine(engine akita.Engine) LastLevelTLBBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the freq the LatTLBs use
func (b LastLevelTLBBuilder) WithFreq(freq akita.Freq) LastLevelTLBBuilder {
	b.freq = freq
	return b
}

// WithNumSets sets the number of sets in a LatTLB. Use 1 for fully associated
// LatTLBs.
func (b LastLevelTLBBuilder) WithNumSets(n int) LastLevelTLBBuilder {
	b.numSets = n
	return b
}

// WithNumWays sets the number of ways in a LatTLB. Set this field to the number
// of LatTLB entries for all the functions.
func (b LastLevelTLBBuilder) WithNumWays(n int) LastLevelTLBBuilder {
	b.numWays = n
	return b
}

// WithPageSize sets the page size that the LatTLB works with.
func (b LastLevelTLBBuilder) WithPageSize(n uint64) LastLevelTLBBuilder {
	b.pageSize = n
	return b
}

// WithNumReqPerCycle sets the number of requests per cycle can be processed by
// a LatTLB
func (b LastLevelTLBBuilder) WithNumReqPerCycle(n int) LastLevelTLBBuilder {
	b.numReqPerCycle = n
	return b
}

// WithNumMSHREntry sets the number of mshr entry
func (b LastLevelTLBBuilder) WithNumMSHREntry(num int) LastLevelTLBBuilder {
	b.numMSHREntry = num
	return b
}

// WithLatency sets the number of mshr entry
func (b LastLevelTLBBuilder) WithLatency(latency int) LastLevelTLBBuilder {
	b.latency = latency
	return b
}

// WithIndexingMask sets the bits to use for indexing
func (b LastLevelTLBBuilder) WithLog2PageSize(log2PageSize uint64) LastLevelTLBBuilder {
	b.log2PageSize = log2PageSize
	return b
}

func (b LastLevelTLBBuilder) UseCoalescingTLBPort() LastLevelTLBBuilder {
	b.useCoalescingTLBPort = true
	return b
}

// WithPageWalkCacheSize sets the size of the page walk cache in bytes.
func (b LastLevelTLBBuilder) WithPageWalkCacheSize(size uint64) LastLevelTLBBuilder {
	b.pageWalkCacheSize = size
	return b
}

// WithDispatchPolicy sets the dispatch policy of the TLB.
func (b LastLevelTLBBuilder) WithDispatchPolicy(policy string) LastLevelTLBBuilder {
	b.dispatchPolicy = policy
	return b
}

// Build creates a new LatTLB
func (b LastLevelTLBBuilder) Build(name string) TLB {
	tlb := &LastLevelTLB{}
	tlb.TickingComponent =
		akita.NewTickingComponent(name, b.engine, b.freq, tlb)

	tlb.numSets = b.numSets

	tlb.log2NumSets = uint64(math.Log2(float64(tlb.numSets)))
	tlb.setMask = uint64(tlb.numSets - 1)
	tlb.numTerms = (48-b.log2PageSize)/tlb.log2NumSets - 1

	tlb.numWays = b.numWays
	tlb.numReqPerCycle = b.numReqPerCycle
	tlb.pageSize = b.pageSize
	tlb.latency = b.latency
	if b.log2PageSize == 0 {
		panic("need to set page size in tlb!")
	}
	tlb.log2PageSize = b.log2PageSize
	if b.useCoalescingTLBPort {
		// tlb.TopPort = NewCoalescingPort(tlb, 16*b.numReqPerCycle,
		// 	name+".TopPort")
		tlb.TopPort = NewCoalescingPort(tlb, 512,
			name+".TopPort")
	} else {
		// tlb.TopPort = akita.NewLimitNumMsgPort(tlb, 16*b.numReqPerCycle,
		// name+".TopPort")
		tlb.TopPort = akita.NewLimitNumMsgPort(tlb, 512, name+".TopPort")
	}
	tlb.BottomPort = akita.NewLimitNumMsgPort(tlb, b.numReqPerCycle,
		name+".BottomPort")
	tlb.ControlPort = akita.NewLimitNumMsgPort(tlb, 1,
		name+".ControlPort")
	tlb.mshr = newMSHR(b.numMSHREntry)
	tlb.extensionmshr = newMSHR(0)
	tlb.lookupBuffer = util.NewBuffer(2 * tlb.numReqPerCycle)
	pipelineBuilder := pipelining.MakeBuilder().WithPipelineWidth(tlb.numReqPerCycle).WithNumStage(tlb.latency).WithCyclePerStage(1).WithPostPipelineBuffer(tlb.lookupBuffer)
	tlb.pipeline = pipelineBuilder.Build(tlb.Name() + "_pipeline")
	tlb.reset()

	pageWalkCacheBuilder := writeback.MakePageWalkCacheBuilder().
		WithEngine(b.engine).
		WithLog2PageSize(b.log2PageSize).
		WithBitsPerLevel(9).
		WithByteSize(b.pageWalkCacheSize)
	pageWalkCache := pageWalkCacheBuilder.Build(
		fmt.Sprintf("%s.PageWalkCache", name),
	)
	tlb.PageWalkCache = pageWalkCache.TopPort
	tlb.PWCWritePort = pageWalkCache.ToMMUs
	tlb.ToPageWalkCache = akita.NewLimitNumMsgPort(tlb, 4096, name+".ToPageWalkCache")
	mmuToPageWalkCache := akita.NewDirectConnection("MMUToPageWalkCache", b.engine, b.freq)
	mmuToPageWalkCache.PlugIn(pageWalkCache.TopPort, 4)
	mmuToPageWalkCache.PlugIn(tlb.ToPageWalkCache, 4)

	tlb.inflightPageWalkCacheReqs = make(map[string]*device.TranslationReq)

	switch b.dispatchPolicy {
	case "roundrobin":
		tlb.dispatcher = &internal.RoundRobinDispatcher{}
	case "leasefirst":
		tlb.dispatcher = &internal.LeaseFirstDispatcher{}
	case "backtosource":
		tlb.dispatcher = &internal.BackToSourceDispatcher{}
	case "interleaved":
		tlb.dispatcher = &internal.InterleavedDispatcher{
			Offset: tlb.log2PageSize + 6,
		}
	default:
		panic(fmt.Sprintf("unsupported dispatch policy: %s", b.dispatchPolicy))
	}

	return tlb
}

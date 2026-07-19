package builders

import (
	"fmt"
	"log"
	"math"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/idealmemcontroller"
	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/mem/vm/mmu/baseline"
	"gitlab.com/akita/mem/vm/mmu/infinite"
	"gitlab.com/akita/mem/vm/mmu/mpw"
	NBWalkerMMU "gitlab.com/akita/mem/vm/mmu/nbwalker"
	"gitlab.com/akita/mem/vm/tlb"
	"gitlab.com/akita/mgpusim"
	NBWalker "gitlab.com/akita/mgpusim/timing/caches/nbwalker"
	"gitlab.com/akita/mgpusim/yamlconfig"
	"gitlab.com/akita/noc/networking/chipnetwork"
	"gitlab.com/akita/util/tracing"
)

type MGPUSimNUMAGPUBuilder struct {
	*CommonBuilder

	// specific componenets
	useTLBMonitor   bool
	useCaPWQMonitor bool
	ptwTracer       tracing.Tracer
	caPWQTracer     tracing.Tracer

	l1Tol2Connection *akita.DelayedConnection
	l2TLBToL3TLB     *akita.DelayedConnection
}

func (b *MGPUSimNUMAGPUBuilder) WithTLBMonitor() {
	b.useTLBMonitor = true
}

func (b *MGPUSimNUMAGPUBuilder) WithCaPWQMonitor() {
	b.useCaPWQMonitor = true
}

func (b *MGPUSimNUMAGPUBuilder) WithPTWTracer(
	tracer tracing.Tracer,
) {
	b.ptwTracer = tracer
}

func (b *MGPUSimNUMAGPUBuilder) WithCaPWQTracer(
	tracer tracing.Tracer,
) {
	b.caPWQTracer = tracer
}

func MakeMGPUSimNUMAGPUBuilder() MGPUSimNUMAGPUBuilder {
	// TODO: should this be using new? is the object being allocated on the stack?
	cbp := CommonBuilder{}
	b := MGPUSimNUMAGPUBuilder{CommonBuilder: &cbp}
	b.SetDefaultCommonBuilderParams()
	return b
}

func (b MGPUSimNUMAGPUBuilder) Build(name string, id uint64) *mgpusim.GPU {
	b.createGPU(name, id)

	b.buildCP()

	chipRdmaAddressTable := b.createChipRDMAAddrTable()
	rdmaResponsePorts := make([]akita.Port, b.numChiplet)

	chipletName := fmt.Sprintf("%s.chiplet_%02d", b.gpuName, 0)
	chiplet := NewChiplet(chipletName, uint64(0))

	b.BuildConnection(chiplet)

	b.BuildSAs(chiplet)
	b.buildMemBanks(chiplet)
	b.buildL2TLB(chiplet)
	b.buildL3TLB(chiplet)
	b.buildMMU(chiplet)

	b.configChipRDMAEngine(chiplet, chipRdmaAddressTable, rdmaResponsePorts)

	b.establishL1ToL2RoutingPath(chiplet)
	b.establishL1TLBToL2TLBRoutingPath(chiplet)
	b.establishL2TLBToL3TLBRoutingPath(chiplet)
	b.establishMMUToL2RoutingPath(chiplet)

	b.connectL2ToDRAM(chiplet)

	b.chiplets = append(b.chiplets, chiplet)

	b.buildPageMigrationController()
	b.setupDMA()

	b.connectCP()
	b.setupInterchipNetwork()

	b.establishTLBMonitor(chiplet)
	b.establishCaPWQMonitor(chiplet)

	return b.gpu
}

func (b *MGPUSimNUMAGPUBuilder) BuildConnection(chiplet *Chiplet) {
	b.l1Tol2Connection = akita.NewDelayedDirectConnection(
		fmt.Sprintf("%s.L1ToL2Connection", chiplet.name),
		b.engine,
		b.freq,
		akita.GetLatency,
	)

	b.l2TLBToL3TLB = akita.NewDelayedDirectConnection(
		fmt.Sprintf("%s.L2TLBToL3TLB", chiplet.name),
		b.engine,
		b.freq,
		akita.GetLatency,
	)
}

func (b *MGPUSimNUMAGPUBuilder) connectCP() {
	b.internalConn = akita.NewDirectConnection(
		b.gpuName+"InternalConn", b.engine, b.freq)
	b.gpu.InternalConnection = b.internalConn

	b.internalConn.PlugIn(b.cp.ToDriver, 1)
	b.internalConn.PlugIn(b.cp.ToDMA, 128)
	b.internalConn.PlugIn(b.cp.ToCaches, 128)
	b.internalConn.PlugIn(b.cp.ToCUs, 128)
	b.internalConn.PlugIn(b.cp.ToTLBs, 128)
	b.internalConn.PlugIn(b.cp.ToAddressTranslators, 128)
	b.internalConn.PlugIn(b.cp.ToRDMA, 4)
	b.internalConn.PlugIn(b.cp.ToPMC, 4)

	b.internalConn.PlugIn(b.cp.ToRTU, 4)
	b.internalConn.PlugIn(b.cp.ToMMUs, 4)

	b.cp.RDMA = b.rdmaEngine.CtrlPort
	b.internalConn.PlugIn(b.cp.RDMA, 1)

	b.cp.DMAEngine = b.dmaEngine.ToCP
	b.internalConn.PlugIn(b.dmaEngine.ToCP, 1)

	b.cp.PMC = b.pageMigrationController.CtrlPort
	b.internalConn.PlugIn(b.pageMigrationController.CtrlPort, 1)

	b.connectCPWithCUs()
	b.connectCPWithAddressTranslators()
	b.connectCPWithCaches()
	b.connectCPWithTLBs()
}

// BuildSAs builds shader arrays.
func (b *MGPUSimNUMAGPUBuilder) BuildSAs(chiplet *Chiplet) {
	saBuilder := makeShaderArrayBuilder()
	saBuilder.withEngine(b.engine)
	saBuilder.withFreq(b.freq)
	saBuilder.withGPUID(b.gpu.GPUID)
	saBuilder.withLog2CachelineSize(b.log2CacheLineSize)
	saBuilder.withLog2PageSize(b.log2PageSize)
	saBuilder.withNumCU(b.numCUPerShaderArray)
	saBuilder.withPageTable(b.pageTable)

	switch yamlconfig.OverrideConfig["MMU.type"] {
	case "NBWalker":
		saBuilder.withConfig("NBWalker")
	default:
	}

	if b.enableVisTracing {
		saBuilder.withVisTracer(b.visTracer)
	}

	if b.enableTLBTracing {
		saBuilder.withTLBTracer(b.tlbTracer)
	}

	maxCUsPerGPC := 16
	maxSAsPerGPC := maxCUsPerGPC / b.numCUPerShaderArray
	if b.numShaderArrayPerChiplet%maxSAsPerGPC != 0 {
		panic("numShaderArrayPerChiplet should be divisible by maxCUsPerGPC")
	}

	for i := 0; i < b.numShaderArrayPerChiplet; i++ {
		gpcID := i / maxSAsPerGPC
		innerSAID := i % maxSAsPerGPC

		saName := fmt.Sprintf(
			"%s.GPC_%02d.SA_%02d",
			chiplet.name,
			gpcID,
			innerSAID,
		)
		sa := saBuilder.Build(saName, i)
		b.collectSAComponents(sa, chiplet)
	}
}

func (b *MGPUSimNUMAGPUBuilder) establishL1ToL2RoutingPath(chiplet *Chiplet) {
	fmt.Println("memory address offset:", b.memAddrOffset)
	lowModuleFinder := cache.NewStripedLocalVRemoteLowModuleFinder(b.memAddrOffset, uint64(b.numChiplet*b.numMemoryBankPerChiplet),
		1<<b.log2MemoryBankInterleavingSize, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID, uint64(b.numMemoryBankPerChiplet)*chiplet.ChipletID+uint64(b.numMemoryBankPerChiplet-1))
	lowModuleFinder.ModuleForOtherAddresses = chiplet.chipRdmaEngine.ToL1

	for _, l1v := range chiplet.L1VCaches {
		l1v.SetLowModuleFinder(lowModuleFinder)

		b.l1Tol2Connection.PlugIn(l1v.GetBottomPort(), 16)
	}

	for _, l1s := range chiplet.L1SCaches {
		l1s.SetLowModuleFinder(lowModuleFinder)

		b.l1Tol2Connection.PlugIn(l1s.GetBottomPort(), 16)
	}

	for _, l1iAT := range chiplet.L1IAddrTranslator {
		l1iAT.SetLowModuleFinder(lowModuleFinder)

		b.l1Tol2Connection.PlugIn(l1iAT.GetBottomPort(), 16)
	}

	for _, l2 := range chiplet.L2Caches {
		lowModuleFinder.LowModules = append(lowModuleFinder.LowModules,
			l2.TopPort)

		b.l1Tol2Connection.PlugIn(l2.TopPort, 64)
	}
	chiplet.lowModuleFinderForL1 = lowModuleFinder
}

func (b *MGPUSimNUMAGPUBuilder) establishL1TLBToL2TLBRoutingPath(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	for i := 0; i < numGPCs; i++ {
		singeLowModuleFinder := new(cache.SingleLowModuleFinder)
		singeLowModuleFinder.LowModule = chiplet.L2TLBs[i].GetTopPort()

		l1TLBToL2TLB := akita.NewDirectConnection(
			fmt.Sprintf("%s.GPC_%01d.L1TLBToL2TLB", chiplet.name, i),
			b.engine,
			b.freq,
		)

		for j := i * numCUsPerGPC; j < (i+1)*numCUsPerGPC; j++ {
			chiplet.L1VTLBs[j].SetLowModuleFinder(singeLowModuleFinder)

			l1TLBToL2TLB.PlugIn(chiplet.L1VTLBs[j].GetBottomPort(), 16)
		}

		numSAPerGPC := b.numShaderArrayPerChiplet / numGPCs
		if b.numShaderArrayPerChiplet%numGPCs != 0 {
			panic("numShaderArrayPerChiplet not divisible by numGPCs")
		}

		for j := i * numSAPerGPC; j < (i+1)*numSAPerGPC; j++ {
			chiplet.L1STLBs[j].SetLowModuleFinder(singeLowModuleFinder)
			chiplet.L1ITLBs[j].SetLowModuleFinder(singeLowModuleFinder)

			l1TLBToL2TLB.PlugIn(chiplet.L1STLBs[j].GetBottomPort(), 16)
			l1TLBToL2TLB.PlugIn(chiplet.L1ITLBs[j].GetBottomPort(), 16)
		}

		l1TLBToL2TLB.PlugIn(chiplet.L2TLBs[i].GetTopPort(), 64)
	}
}

func (b *MGPUSimNUMAGPUBuilder) establishL2TLBToL3TLBRoutingPath(chiplet *Chiplet) {
	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	singleLowModuleFinder := new(cache.SingleLowModuleFinder)
	singleLowModuleFinder.LowModule = chiplet.L3TLBs[0].GetTopPort()

	for i := 0; i < numGPCs; i++ {
		chiplet.L2TLBs[i].SetLowModuleFinder(singleLowModuleFinder)

		b.l2TLBToL3TLB.PlugIn(chiplet.L2TLBs[i].GetBottomPort(), 32)
	}

	chiplet.L3TLBs[0].SetTLBFinder(singleLowModuleFinder)
	b.l2TLBToL3TLB.PlugIn(chiplet.L3TLBs[0].GetTopPort(), 128)
}

func (b *MGPUSimNUMAGPUBuilder) establishMMUToL2RoutingPath(chiplet *Chiplet) {
	b.l2TLBToL3TLB.PlugIn(chiplet.L3TLBs[0].GetBottomPort(), 64)
	b.l2TLBToL3TLB.PlugIn(chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort, 64)

	for _, mmu := range chiplet.MMUs {
		mmu.SetLowModuleFinder(chiplet.lowModuleFinderForL1)

		b.l1Tol2Connection.PlugIn(mmu.ToTranslationPort(), 64)
		b.l2TLBToL3TLB.PlugIn(mmu.ToPageWalkCachePort(), 64)
		b.l2TLBToL3TLB.PlugIn(mmu.ToTopPort(), 64)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildMemBanks(chiplet *Chiplet) {
	l2Builder := writeback.MakeBuilder().
		WithEngine(b.engine).
		WithFreq(b.freq).
		WithLog2BlockSize(b.log2CacheLineSize).
		WithWayAssociativity(16).
		WithByteSize(128 * mem.KB).
		WithNumMSHREntry(32).
		WithNumReqPerCycle(4).
		WithBankLatency(10).
		WithPipelineLatency(80).
		WithNumBanks(1)

	maxSlicesPerPartition := 8
	if b.numMemoryBankPerChiplet%maxSlicesPerPartition != 0 {
		panic("numMemoryBankPerChiplet should be divisible by maxSlicesPerPartition")
	}

	for i := 0; i < b.numMemoryBankPerChiplet; i++ {
		partitionID := i / maxSlicesPerPartition
		innerID := i % maxSlicesPerPartition

		dramName := fmt.Sprintf(
			"%s.MP_%02d.DRAM_%d",
			chiplet.name,
			partitionID,
			innerID,
		)
		dram := idealmemcontroller.New(
			dramName, b.engine, 512*mem.MB)
		addrConverter := idealmemcontroller.InterleavingConverter{
			InterleavingSize:    1 << b.log2MemoryBankInterleavingSize,
			TotalNumOfElements:  b.numChiplet * b.numMemoryBankPerChiplet,
			CurrentElementIndex: b.numMemoryBankPerChiplet*int(chiplet.ChipletID) + i,
			Offset:              b.memAddrOffset,
			//  + b.memoryPerChiplet*chiplet.ChipletID,
		}
		// fmt.Println("^^^^^", b.numMemoryBankPerChiplet*int(chiplet.ChipletID)+i)
		dram.AddressConverter = addrConverter

		b.drams = append(b.drams, dram)
		b.gpu.MemoryControllers = append(b.gpu.MemoryControllers, dram)
		chiplet.DRAMs = append(chiplet.DRAMs, dram)

		if b.enableVisTracing {
			tracing.CollectTrace(dram, b.visTracer)
		}

		cacheName := fmt.Sprintf(
			"%s.MP_%02d.L2_%02d",
			chiplet.name,
			partitionID,
			innerID,
		)
		l2 := l2Builder.Build(cacheName)
		b.l2Caches = append(b.l2Caches, l2)
		b.gpu.L2Caches = append(b.gpu.L2Caches, l2)
		chiplet.L2Caches = append(chiplet.L2Caches, l2)
		l2.SetLowModuleFinder(&cache.SingleLowModuleFinder{
			LowModule: dram.ToTop,
		})
		if b.enableVisTracing {
			tracing.CollectTrace(l2, b.visTracer)
		}
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildL2TLB(chiplet *Chiplet) {
	numSets := 64
	numWays := 8
	log2NumSets := int(math.Log2(float64(numSets)))

	tlbIndexBitsStart := int(math.Log2(float64(b.remoteTLBInterleavingSize))) + int(b.log2PageSize) + 1
	tlbIndexBitsEnd := tlbIndexBitsStart + int(math.Log2(float64(b.numChiplet))) - 1

	mask := uint64(0)
	t := uint64(1) << b.log2PageSize
	numBitsSet := 0
	for i := int(b.log2PageSize) + 1; i <= 64; i++ {
		if i < tlbIndexBitsStart || i > tlbIndexBitsEnd {
			mask = mask | t
			numBitsSet++
			if numBitsSet == log2NumSets {
				break
			}
		}
		t = t << 1
	}

	numCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numCUsPerGPC + 1

	for i := 0; i < numGPCs; i++ {
		builder := tlb.MakeLatTLBBuilder().
			WithEngine(b.engine).
			WithFreq(b.freq).
			WithNumWays(numWays).
			WithNumSets(numSets).
			WithNumMSHREntry(128).
			WithNumReqPerCycle(4).
			WithLog2PageSize(b.log2PageSize).
			WithIndexingMask(mask).
			WithLatency(40)

		if b.useCoalescingTLBPort {
			builder = builder.UseCoalescingTLBPort()
		}
		l2TLB := builder.Build(fmt.Sprintf("%s.GPC_%02d_L2TLB", chiplet.name, i))

		b.l2TLBs = append(b.l2TLBs, l2TLB)
		b.gpu.L2TLBs = append(b.gpu.L2TLBs, l2TLB)
		chiplet.L2TLBs = append(chiplet.L2TLBs, l2TLB)

		if b.enableVisTracing {
			tracing.CollectTrace(l2TLB, b.visTracer)
		}
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildL3TLB(chiplet *Chiplet) {
	numSets := 256
	numWays := 8
	log2NumSets := int(math.Log2(float64(numSets)))

	tlbIndexBitsStart := int(math.Log2(float64(b.remoteTLBInterleavingSize))) + int(b.log2PageSize) + 1
	tlbIndexBitsEnd := tlbIndexBitsStart + int(math.Log2(float64(b.numChiplet))) - 1

	mask := uint64(0)
	t := uint64(1) << b.log2PageSize
	numBitsSet := 0
	for i := int(b.log2PageSize) + 1; i <= 64; i++ {
		if i < tlbIndexBitsStart || i > tlbIndexBitsEnd {
			mask = mask | t
			numBitsSet++
			if numBitsSet == log2NumSets {
				break
			}
		}
		t = t << 1
	}

	dispatchPolicy := yamlconfig.OverrideConfig["L3TLB.dispatcher"]

	builder := tlb.MakeLastLevelTLBBuilder().
		WithEngine(b.engine).
		WithFreq(b.freq).
		WithNumWays(numWays).
		WithNumSets(numSets).
		WithNumMSHREntry(512).
		WithNumReqPerCycle(8).
		WithLog2PageSize(b.log2PageSize).
		WithPageWalkCacheSize(2048).
		WithDispatchPolicy(dispatchPolicy).
		WithLatency(80)

	if b.useCoalescingTLBPort {
		builder = builder.UseCoalescingTLBPort()
	}
	l3TLB := builder.Build(fmt.Sprintf("%s.L3TLB", chiplet.name))

	b.l3TLBs = append(b.l3TLBs, l3TLB)
	b.gpu.L3TLBs = append(b.gpu.L3TLBs, l3TLB)
	chiplet.L3TLBs = append(chiplet.L3TLBs, l3TLB)

	if b.enableVisTracing {
		tracing.CollectTrace(l3TLB, b.visTracer)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildMMU(chiplet *Chiplet) {
	if yamlconfig.OverrideConfig == nil {
		b.buildDefaultMMU(chiplet)
	} else {
		mmuType := yamlconfig.OverrideConfig["MMU.type"]

		switch mmuType {
		case "BaselineMMU":
			b.buildDefaultMMU(chiplet)
		case "IdealMMU":
			b.buildIdealMMU(chiplet)
		case "InfiniteMMU":
			b.buildInfiniteMMU(chiplet)
		case "MPWMMU":
			b.buildMPWMMU(chiplet)
		case "NBWalker":
			b.buildNBWalkerMMU(chiplet)
		default:
			log.Panicf("Unsupported MMU type: %s\n", mmuType)
		}
	}

	for _, mmu := range chiplet.MMUs {
		if b.ptwTracer != nil {
			tracing.CollectTrace(mmu, b.ptwTracer)
		}

		b.l3TLBs[0].(*tlb.LastLevelTLB).RegisterMMU(
			mmu.ToTopPort(),
		)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildDefaultMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := baseline.MakeMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.BaselineMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*baseline.MMUImpl).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildInfiniteMMU(chiplet *Chiplet) {
	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	for i := 0; i < numGPCs; i++ {
		component := infinite.MakeMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			Build(fmt.Sprintf("%s.GPC_%02d.InfiniteMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*infinite.MMUImpl).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildMPWMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := mpw.MakeMPWMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.mpwMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*mpw.MPWMMU).PageWalkCache = pageWalkCachePort

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildIdealMMU(chiplet *Chiplet) {
	pageWalkLatency := 200
	maxActiveWalkers := 0

	if latency, ok := yamlconfig.OverrideConfig["MMU.walkLatency"]; ok {
		latencyInt, err := strconv.Atoi(latency)
		if err != nil {
			log.Panicf("Invalid walk latency: %v\n", latency)
		}

		pageWalkLatency = latencyInt
	}

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxActiveWalkers = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxActiveWalkers%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := mmu.MakeIdealMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithLatency(pageWalkLatency).
			WithMaxActiveTransactions(uint64(maxActiveWalkers / numGPCs)).
			Build(fmt.Sprintf("%s.GPC_%02d.IdealMMU", chiplet.name, i))

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}
}

func (b *MGPUSimNUMAGPUBuilder) buildNBWalkerMMU(chiplet *Chiplet) {
	maxNumReqInFlight := 16

	if numWalkers, ok := yamlconfig.OverrideConfig["MMU.numPageWalkers"]; ok {
		numWalkersInt, err := strconv.Atoi(numWalkers)
		if err != nil {
			log.Panicf("Invalid number of walkers %v\n", numWalkersInt)
		}

		maxNumReqInFlight = numWalkersInt
	}

	maxCUsPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/maxCUsPerGPC + 1

	if maxNumReqInFlight%numGPCs != 0 {
		panic("numPageWalkers should be divisible by numGPCs")
	}

	for i := 0; i < numGPCs; i++ {
		component := NBWalkerMMU.MakeNBWalkerMMUBuilder().
			WithEngine(b.engine).
			WithFreq(1 * akita.GHz).
			WithLog2PageSize(b.log2PageSize).
			WithPageTable(b.pageTable).
			WithMaxNumReqInFlight(maxNumReqInFlight / numGPCs).
			Build(fmt.Sprintf("%s.GPC_%02d.NBWalkerMMU", chiplet.name, i))

		pageWalkCachePort := chiplet.L3TLBs[0].(*tlb.LastLevelTLB).PWCWritePort

		component.(*NBWalkerMMU.NBWalkerMMU).PageWalkCache = pageWalkCachePort
		component.(*NBWalkerMMU.NBWalkerMMU).L3TLB = chiplet.L3TLBs[0].GetBottomPort()

		chiplet.MMUs = append(chiplet.MMUs, component)
		b.gpu.MMUs = append(b.gpu.MMUs, component)
	}

	b.establishMMUToNBWalkerL1RoutingPath(chiplet)
}

func (b *MGPUSimNUMAGPUBuilder) establishMMUToNBWalkerL1RoutingPath(chiplet *Chiplet) {
	numVCachesPerGPC := 16
	numGPCs := (len(chiplet.CUs)-1)/numVCachesPerGPC + 1

	if len(chiplet.MMUs) != numGPCs {
		panic("number of MMUs should be the same as number of GPCs")
	}

	for i := 0; i < numGPCs; i++ {
		vlowModuleFinder := cache.NewXORLowModuleFinder(
			numVCachesPerGPC,
			4,
			int(math.Log2(float64(numVCachesPerGPC))),
			int(b.log2CacheLineSize))

		vControlFinder := cache.NewXORLowModuleFinder(
			numVCachesPerGPC,
			4,
			int(math.Log2(float64(numVCachesPerGPC))),
			int(b.log2CacheLineSize))

		switch mmu := chiplet.MMUs[i].(type) {
		case *NBWalkerMMU.NBWalkerMMU:
			mmu.VCacheLowModuleFinder = vlowModuleFinder
			mmu.VCacheControlFinder = vControlFinder
		default:
			panic("MMU is not NBWalkerMMU")
		}

		conn := akita.NewDirectConnection(
			fmt.Sprintf("%s.MMU_%02d_To_L1Conn", chiplet.name, i),
			b.engine, 1*akita.GHz,
		)

		conn.PlugIn(chiplet.MMUs[i].ToCachePort(), 16)

		for j := i * numVCachesPerGPC; j < (i+1)*numVCachesPerGPC; j++ {
			vlowModuleFinder.LowModules = append(
				vlowModuleFinder.LowModules,
				chiplet.L1VCaches[j].GetWalkerPort(),
			)
			conn.PlugIn(chiplet.L1VCaches[j].GetWalkerPort(), 16)

			vControlFinder.LowModules = append(
				vControlFinder.LowModules,
				chiplet.L1VCaches[j].GetControlPort(),
			)
			conn.PlugIn(chiplet.L1VCaches[j].GetControlPort(), 16)

			chiplet.L1VCaches[j].(*NBWalker.Cache).PageWalker =
				chiplet.MMUs[i].ToCachePort()
		}
	}
}

func (b *MGPUSimNUMAGPUBuilder) setupInterchipNetwork() {
	chipConnector := chipnetwork.NewInterChipletConnector().
		WithEngine(b.engine).
		WithSwitchLatency(360).
		WithFreq(1 * akita.GHz).
		WithFlitByteSize(64).
		WithNumReqPerCycle(12).
		WithNetworkName("ICN")
	chipConnector.CreateNetwork()
	for _, chiplet := range b.chiplets {
		chipConnector.PlugInChip(b.InterChipletPorts(chiplet))
	}
	chipConnector.MakeNetwork()
}

func (b *MGPUSimNUMAGPUBuilder) InterChipletPorts(c *Chiplet) []akita.Port {
	ports := []akita.Port{
		c.chipRdmaEngine.RequestPort,
		c.chipRdmaEngine.ResponsePort,
	}
	return ports
}

func (b *MGPUSimNUMAGPUBuilder) establishTLBMonitor(c *Chiplet) {
	if !b.useTLBMonitor {
		return
	}

	tlbMonitor := monitor.NewTLBMonitor(
		fmt.Sprintf("%s.TLBMonitor", b.gpuName),
		b.engine,
		1*akita.MHz,
	)

	for _, l3tlb := range c.L3TLBs {
		tlbMonitor.RegisterL3TLB(l3tlb.(monitor.MonitorComponent))
	}

	b.gpu.TLBMonitors = append(b.gpu.TLBMonitors, tlbMonitor)
}

func (b *MGPUSimNUMAGPUBuilder) establishCaPWQMonitor(c *Chiplet) {
	if !b.useCaPWQMonitor {
		return
	}

	caPWQMonitor := monitor.NewCaPWQMonitor(
		fmt.Sprintf("%s.CaPWQMonitor", b.gpuName),
		b.engine,
		1*akita.MHz,
	)

	for _, l1v := range c.L1VCaches {
		caPWQMonitor.RegisterL1VCache(l1v.(monitor.MonitorComponent))
	}

	for _, mmu := range c.MMUs {
		switch walker := mmu.(type) {
		case *baseline.MMUImpl:
			caPWQMonitor.RegisterPageWalker(walker)
		case *NBWalkerMMU.NBWalkerMMU:
			caPWQMonitor.RegisterPageWalker(walker)
		}
	}

	b.gpu.CaPWQMonitor = append(b.gpu.CaPWQMonitor, caPWQMonitor)

	tracing.CollectTrace(caPWQMonitor, b.caPWQTracer)
}

package tlb

import (
	"fmt"
	"log"
	"strings"

	// "math"
	"reflect"
	"strconv"

	// "strings"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/mem/vm/tlb/internal"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

// A LastLevelTLB is a cache that maintains some page information.
type LastLevelTLB struct {
	*akita.TickingComponent

	TopPort         akita.Port
	BottomPort      akita.Port
	ControlPort     akita.Port
	ToRTU           akita.Port
	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	PWCWritePort    akita.Port

	LowModuleFinder cache.LowModuleFinder
	TLBFinder       cache.LowModuleFinder

	HashFuncHSL func(address uint64) uint64

	log2PageSize   uint64
	log2NumSets    uint64
	setMask        uint64
	numTerms       uint64
	numSets        int
	numWays        int
	pageSize       uint64
	numReqPerCycle int
	latency        int
	indexingMask   uint64

	Sets []internal.Set

	pipeline     pipelining.Pipeline
	lookupBuffer util.Buffer

	mshr                mshr
	extensionmshr       mshr
	respondingMSHREntry *mshrEntry

	isPaused bool

	CommandProcessor akita.Port
	stats            TLBStats

	setsAccessed [256]uint64
	mode4Kstrip  bool
	hysterisis   bool

	gpcID int

	monitorStats *monitor.CaPWQMonitorStats

	inflightPageWalkCacheReqs map[string]*device.TranslationReq

	dispatcher   internal.Dispatcher
	cuDispatcher internal.Dispatcher

	useSoftWalker bool
}

func (tlb *LastLevelTLB) SentCommand(info interface{}) {
	if info.(bool) {
		tlb.useSoftWalker = true
	} else {
		tlb.useSoftWalker = false
	}
}

func (tlb *LastLevelTLB) InitMonitorStats() {
	tlb.monitorStats = &monitor.CaPWQMonitorStats{
		Name:   tlb.Name(),
		NumPTW: 0,
	}
}

func (tlb *LastLevelTLB) ClearMonitorStats() {
	tlb.monitorStats.Clear()
}

func (tlb *LastLevelTLB) GetMonitorStats() interface{} {
	return tlb.monitorStats
}

func (tlb *LastLevelTLB) SetMaxExtensionMisses(num int) {
	tlb.extensionmshr = newMSHR(num)
}

// GetPipeline gets the pipeline in the LastLevelTLB
func (tlb *LastLevelTLB) GetPipeline() pipelining.Pipeline {
	return tlb.pipeline
}

// GetPipeline gets the pipeline in the LastLevelTLB
func (tlb *LastLevelTLB) GetTopPort() akita.Port {
	return tlb.TopPort
}

// GetPipeline gets the pipeline in the LastLevelTLB
func (tlb *LastLevelTLB) GetBottomPort() akita.Port {
	return tlb.BottomPort
}

// GetPipeline gets the pipeline in the LastLevelTLB
func (tlb *LastLevelTLB) GetControlPort() akita.Port {
	return tlb.ControlPort
}

// GetNumSets gets the number of sets in the LastLevelTLB
func (tlb *LastLevelTLB) GetNumSets() int {
	return tlb.numSets
}

// GetNumWays gets the number of ways in the LastLevelTLB
func (tlb *LastLevelTLB) GetNumWays() int {
	return tlb.numWays
}

func (tlb *LastLevelTLB) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	tlb.LowModuleFinder = lmf
}

func (tlb *LastLevelTLB) GetLowModuleFinder() cache.LowModuleFinder {
	return tlb.LowModuleFinder
}

func (tlb *LastLevelTLB) SetCommandProcessor(cp akita.Port) {
	tlb.CommandProcessor = cp
}

func (tlb *LastLevelTLB) SetGPCID(id int) {
	tlb.gpcID = id
}

func (tlb *LastLevelTLB) RegisterMMU(mmu akita.Port) {
	tlb.dispatcher.Register(mmu)
}

func (tlb *LastLevelTLB) RegisterCU(cu akita.Port) {
	tlb.cuDispatcher.Register(cu)
}

// Reset sets all the entries int he LastLevelTLB to be invalid
func (tlb *LastLevelTLB) reset() {
	tlb.Sets = make([]internal.Set, tlb.numSets)
	for i := 0; i < tlb.numSets; i++ {
		set := internal.NewSet(tlb.numWays)
		tlb.Sets[i] = set
	}
}

// Tick defines how LastLevelTLB update states at each cycle
func (tlb *LastLevelTLB) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = tlb.performCtrlReq(now) || madeProgress

	tlb.doStatsCollection(now)
	// move the call to doStatsCollection to three places where
	// numAccesses is being incremented.

	// improper use of StartTask
	if tlb.GetFrontQueueLength() > 0 {
		tracing.StartTask("", "", now, tlb, "imbalance", "", nil)
	}

	if !tlb.isPaused {
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.respondMSHREntry(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseFromPageWalkCache(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseBottom(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.lookup(now) || madeProgress
		}

		madeProgress = tlb.pipeline.Tick(now) || madeProgress

		// pipeline or queue cycling done here

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseFromTop(now) || madeProgress
		}

		if tlb.monitorStats != nil {
			tlb.monitorStats.NumPTW = uint64(len(tlb.mshr.AllEntries()))
		}
	}
	return madeProgress
}

func (tlb *LastLevelTLB) respondMSHREntry(now akita.VTimeInSec) bool {
	if tlb.respondingMSHREntry == nil {
		return false
	}

	mshrEntry := tlb.respondingMSHREntry
	page := mshrEntry.page
	req := mshrEntry.Requests[0]

	var accessResult device.AccessResult
	if mshrEntry.NumResponded() == 0 {
		accessResult = device.TLBMiss
		tlb.stats.missesReturned++
		if tlb.stats.interleaving == 12 && !tlb.stats.sendStateInfo && tlb.stats.missesReturned == 1300 {
			tlb.stats.sendStateInfo = true
		}
		// tlb.stats.missesReturnedInCurEpoch++
		// fmt.Println(tlb.stats.missesReturnedInCurEpoch)
	} else {
		accessResult = device.TLBMshrHit
	}
	rspToTop := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.TopPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		WithAccessResult(accessResult).
		WithSrcL2TLB(tlb.Name()).
		WithGPCID(tlb.gpcID).
		Build()
	err := tlb.TopPort.Send(rspToTop)
	if err != nil {
		return false
	}
	mshrEntry.IncNumRespondedByOne()
	mshrEntry.Requests = mshrEntry.Requests[1:]
	if len(mshrEntry.Requests) == 0 {
		tlb.respondingMSHREntry = nil
	}
	tracing.StartTracingNetworkTLBReq(rspToTop, now, tlb, req, tlb.gpcID)
	tracing.TraceReqComplete(req, now, tlb)
	return true
}

func (tlb *LastLevelTLB) collectMSHROccupancy(now akita.VTimeInSec) {
	m := tlb.mshr
	uniqEntries := len(m.AllEntries())
	totalEntries := 0
	for _, me := range m.AllEntries() {
		totalEntries += len(me.Requests)
	}
	// fmt.Println("MSHR len:", uniqEntries)
	tracing.StartTask("", "", now, tlb,
		"MSHRlen", strconv.Itoa(totalEntries), nil)
	tracing.StartTask("", "", now, tlb,
		"MSHRuniq", strconv.Itoa(uniqEntries), nil)
	if uniqEntries > 0 {
		tracing.StartTask("", "", now, tlb,
			"MSHRlen_g0", strconv.Itoa(totalEntries), nil)
		tracing.StartTask("", "", now, tlb,
			"MSHRuniq_g0", strconv.Itoa(uniqEntries), nil)
	}
}

func (tlb *LastLevelTLB) parseFromTop(now akita.VTimeInSec) bool {
	msg := tlb.TopPort.Peek()
	collectCoalescingStat(tlb, now)
	tlb.collectMSHROccupancy(now)
	if msg == nil {
		return false
	}

	req := msg.(*device.TranslationReq)

	// push to waiting queue
	if tlb.pipeline.CanAccept() {
		/*tlb.stats.sendStateInfo && */
		if now-req.SendTime > 1e-9 {

			tlb.stats.numStalledInCurEpoch++

			tracing.AddTaskStep(
				tracing.MsgIDAtReceiver(req, tlb),
				now, tlb,
				"stalled-l3-tlb-req-count",
			)
		}

		tracing.AddTaskStep(
			tracing.MsgIDAtReceiver(req, tlb),
			now, tlb,
			"l3-tlb-req-count",
		)

		pipelineItem := tlbPipelineItem{
			taskID:         akita.GetIDGenerator().Generate(),
			translationReq: req,
		}
		tlb.pipeline.Accept(now, pipelineItem)
		tlb.TopPort.Retrieve(now)
		tracing.TraceReqReceive(req, now, tlb)
		tracing.StopTracingNetworkTLBReq(req, now, tlb, tlb.gpcID)
		tracing.StartTask(strconv.FormatUint(req.VAddr, 10), "", now, tlb, "entropy", "", nil)
		return true
	}
	return false
}

func (tlb *LastLevelTLB) updateReverseSwitchStats(vAddr uint64) {
	if (tlb.stats.epochCountWhatIf % 5000) == 0 {
		if balanceCheck(tlb.stats.perEpochWhatIf) {
			if tlb.hysterisis == true {
				fmt.Println("balanced twice")
				panic("not really implemented yet!")
			} else {
				tlb.hysterisis = true
				fmt.Println("balanced once")
			}
		} else {
			tlb.hysterisis = false
			fmt.Println("not balanced")
		}
		for i := 0; i < 4; i++ {
			fmt.Println(tlb.Name(), tlb.stats.perEpochWhatIf[i])
			tlb.stats.perEpochWhatIf[i] = 0
		}
	}
	tlb.stats.epochCountWhatIf += 1
	tlb.stats.perEpochWhatIf[tlb.HashFuncHSL(vAddr)] += 1

}

func (tlb *LastLevelTLB) lookup(now akita.VTimeInSec) bool {
	// pop from waiting queue
	item := tlb.lookupBuffer.Peek()
	if item == nil {
		return false
	}
	pipelineItem := item.(tlbPipelineItem)
	req := pipelineItem.translationReq
	// if req.VAddr == 49152 {
	// fmt.Println(req.VAddr)
	// panic("oh no! dbchbbhw")
	// }
	mshrEntry := tlb.mshr.Query(req.PID, req.VAddr)
	if mshrEntry != nil {
		ok := tlb.processTLBMSHRHit(now, mshrEntry, req)
		if ok {
			tracing.AddTaskDetailedStep(
				tracing.MsgIDAtReceiver(req, tlb),
				now, tlb,
				"tlb-mshr-hit",
				req.VAddr,
			)

			tlb.lookupBuffer.Pop()
			// if tlb.stats.sendStateInfo {
			tlb.stats.numAccess += 1
			tlb.stats.accessesInCurEpoch++
			tlb.stats.numMSHR += 1
			if tlb.mode4Kstrip {
				tlb.updateReverseSwitchStats(req.VAddr)
			}
			// }
			return true
		}
		return false
	}

	setID := tlb.vAddrToSetID(req.VAddr)
	// setID := tlb.vAddrToSetIDxor7(req.VAddr)
	set := tlb.Sets[setID]
	wayID, page, found := set.Lookup(req.PID, req.VAddr)
	if found && page.Valid {
		return tlb.handleTranslationHit(now, req, setID, wayID, page)
	}

	// passing setID for tracing purposes
	// TODO: use the return value of the following function to avoid passing setID
	return tlb.handleTranslationMiss(now, req, setID)
}

func (tlb *LastLevelTLB) handleTranslationHit(
	now akita.VTimeInSec,
	req *device.TranslationReq,
	setID, wayID int,
	page device.Page,
) bool {
	ok := tlb.sendRspToTop(now, req, page)
	if !ok {
		return false
	}
	tlb.visit(setID, wayID)
	tlb.lookupBuffer.Pop()

	// if tlb.stats.sendStateInfo {
	tlb.stats.numAccess += 1
	tlb.stats.accessesInCurEpoch++
	tlb.stats.numHit += 1
	tlb.stats.hitsInCurEpoch++
	// }
	// fmt.Println("hits:", tlb.stats.hitsInCurEpoch)

	tracing.AddTaskDetailedStep(
		tracing.MsgIDAtReceiver(req, tlb),
		now, tlb,
		"tlb-hit",
		req.VAddr,
	)
	tracing.TraceReqComplete(req, now, tlb)

	if tlb.mode4Kstrip {
		tlb.updateReverseSwitchStats(req.VAddr)
	}
	return true
}

func (tlb *LastLevelTLB) handleTranslationMiss(
	now akita.VTimeInSec,
	req *device.TranslationReq,
	setID int,
) bool {
	if tlb.mshr.IsFull() {
		tracing.StartTask(tlb.Name()+"stall", "", now, tlb, "mshr_stall", "", nil)
		tlb.stats.numMSHRStallsInCurEpoch++
		return false
	}

	fetched := tlb.fetchBottom(now, req)
	if fetched {
		//tlb.TopPort.Retrieve(now)
		tlb.lookupBuffer.Pop()

		// if tlb.stats.sendStateInfo {
		tlb.stats.numAccess += 1
		tlb.stats.accessesInCurEpoch++
		tlb.stats.numMiss += 1
		tlb.stats.missesInCurEpoch++
		// }
		// tracing.TraceReqReceive(req, now, tlb)
		tracing.AddTaskDetailedStep(
			tracing.MsgIDAtReceiver(req, tlb),
			now, tlb,
			"tlb-miss",
			req.VAddr,
		)

		// this is the missepoint
		// add a set miss tracer here.
		tracing.StartTask("", "", now, tlb, "set_miss_tracing",
			strconv.FormatUint(uint64(setID), 10), nil)
		if tlb.mode4Kstrip {
			tlb.updateReverseSwitchStats(req.VAddr)
		}

		return true
	}

	return false
}

// we are assuimg 4K pages here. TODO: FIX THIS!
func (tlb *LastLevelTLB) vAddrToSetIDxor7(vAddr uint64) (setID int) {
	index := uint64(0)
	sp1 := (vAddr >> 12) & 0x7f
	sp2 := (vAddr >> 19) & 0x7f
	sp3 := (vAddr >> 26) & 0x7f
	sp4 := (vAddr >> 33) & 0x7f
	index = uint64(sp1 ^ sp2 ^ sp3 ^ sp4)
	return int(index)
}

func (tlb *LastLevelTLB) vAddrToSetID(vAddr uint64) (setID int) {
	vpn := vAddr >> tlb.log2PageSize
	index := vpn & tlb.setMask
	for i := uint64(0); i < tlb.numTerms; i++ {
		vpn >>= tlb.log2NumSets
		index ^= (vpn & tlb.setMask)
	}
	setID = int(index)
	tlb.setsAccessed[setID]++
	return
}

func (tlb *LastLevelTLB) sendRspToTop(
	now akita.VTimeInSec,
	req *device.TranslationReq,
	page device.Page,
) bool {
	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.TopPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		WithAccessResult(device.TLBHit).
		WithSrcL2TLB(tlb.Name()).
		WithGPCID(tlb.gpcID).
		Build()

	err := tlb.TopPort.Send(rsp)
	if err == nil {
		tracing.StartTracingNetworkTLBReq(rsp, now, tlb, req, tlb.gpcID)
		return true
	}
	return false
}

func (tlb *LastLevelTLB) processTLBMSHRHit(
	now akita.VTimeInSec,
	mshrEntry *mshrEntry,
	req *device.TranslationReq,
) bool {
	// if len(mshrEntry.Requests) < 8 {
	mshrEntry.Requests = append(mshrEntry.Requests, req)

	return true
	// }
	// return false
}

func (tlb *LastLevelTLB) fetchBottom(now akita.VTimeInSec, req *device.TranslationReq) bool {
	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.ToPageWalkCache).
		WithDst(tlb.PageWalkCache).
		WithPID(req.PID).
		WithAddress(req.VAddr).
		WithByteSize(8).
		Build()
	err := tlb.ToPageWalkCache.Send(readReq)
	if err != nil {
		return false
	}

	mshrEntry := tlb.mshr.Add(req.PID, req.VAddr)
	mshrEntry.Requests = append(mshrEntry.Requests, req)

	tlb.inflightPageWalkCacheReqs[readReq.ID] = req

	tlb.stats.avgMSHRLenInCurEpoch = (tlb.stats.avgMSHRLenInCurEpoch*float64(tlb.stats.timesMeasuredInCurEpoch) + float64(len(tlb.mshr.AllEntries()))) / float64(tlb.stats.timesMeasuredInCurEpoch+1)
	tlb.stats.timesMeasuredInCurEpoch++

	return true
}

func (tlb *LastLevelTLB) parseBottom(now akita.VTimeInSec) bool {
	if tlb.respondingMSHREntry != nil {
		return false
	}

	item := tlb.BottomPort.Peek()
	if item == nil {
		return false
	}

	rsp := item.(*device.TranslationRsp)
	page := rsp.Page

	mshrEntryPresent := tlb.mshr.IsEntryPresent(rsp.Page.PID, rsp.Page.VAddr)
	if !mshrEntryPresent {
		tlb.BottomPort.Retrieve(now)
		tracing.TraceReqFinalize(rsp, now, tlb)
		return true
	}

	setID := tlb.vAddrToSetID(page.VAddr)
	// setID := tlb.vAddrToSetIDxor7(page.VAddr)
	set := tlb.Sets[setID]
	wayID, ok := tlb.Sets[setID].Evict()
	if !ok {
		panic("failed to evict")
	}
	set.Update(wayID, page)
	set.Visit(wayID)

	mshrEntry := tlb.mshr.GetEntry(rsp.Page.PID, rsp.Page.VAddr)
	tlb.respondingMSHREntry = mshrEntry
	mshrEntry.page = page

	tlb.mshr.Remove(rsp.Page.PID, rsp.Page.VAddr)

	tlb.stats.avgMSHRLenInCurEpoch = (tlb.stats.avgMSHRLenInCurEpoch*float64(tlb.stats.timesMeasuredInCurEpoch) + float64(len(tlb.mshr.AllEntries()))) / float64(tlb.stats.timesMeasuredInCurEpoch+1)
	tlb.stats.timesMeasuredInCurEpoch++

	tlb.BottomPort.Retrieve(now)
	tracing.TraceReqFinalize(mshrEntry.reqToBottom, now, tlb)

	if strings.Contains(rsp.Src.Name(), "MMU") {
		tlb.dispatcher.Receive(rsp.Src)
	} else {
		tlb.cuDispatcher.Receive(rsp.Src)
	}

	tracing.EndTask(tlb.Name()+"stall", now, tlb)
	return true
}

func (tlb *LastLevelTLB) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := tlb.ToPageWalkCache.Peek()
	if item == nil {
		return false
	}

	if _, ok := item.(*mem.DataReadyRsp); !ok {
		panic(fmt.Sprintf("parseFromPageWalkCache expects DataReadyRsp but got %T", item))
	}

	rsp := item.(*mem.DataReadyRsp)

	req, ok := tlb.inflightPageWalkCacheReqs[rsp.RespondTo]
	if !ok {
		panic("not found!")
	}

	dstPort := tlb.dispatcher.Distribute(req)
	if dstPort == nil {
		dstPort = tlb.cuDispatcher.Distribute(req)
	}

	if dstPort == nil {
		return false
	}

	fetchBottom := device.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.BottomPort).
		WithDst(dstPort).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithDeviceID(req.DeviceID).
		WithData(rsp.Data).
		Build()
	err := tlb.BottomPort.Send(fetchBottom)
	if err != nil {
		if strings.Contains(dstPort.Name(), "MMU") {
			tlb.dispatcher.Receive(dstPort)
		} else {
			tlb.cuDispatcher.Receive(dstPort)
		}

		return false
	}

	mshrEntryPresent := tlb.mshr.IsEntryPresent(req.PID, req.VAddr)
	if !mshrEntryPresent {
		panic("oh no!")
	}

	mshrEntry := tlb.mshr.GetEntry(req.PID, req.VAddr)
	mshrEntry.reqToBottom = fetchBottom

	delete(tlb.inflightPageWalkCacheReqs, rsp.RespondTo)

	tlb.ToPageWalkCache.Retrieve(now)

	tracing.TraceReqInitiate(fetchBottom, now, tlb,
		tracing.MsgIDAtReceiver(req, tlb))

	return true
}

func (tlb *LastLevelTLB) performCtrlReq(now akita.VTimeInSec) bool {
	item := tlb.ControlPort.Peek()
	if item == nil {
		return false
	}

	var madeProgress bool
	switch req := item.(type) {
	case *TLBFlushReq:
		madeProgress = tlb.handleTLBFlush(now, req)
	case *TLBRestartReq:
		madeProgress = tlb.handleTLBRestart(now, req)
	case *akita.TLBIndexingSwitchMsg:
		madeProgress = tlb.switchIndexing(now, req)
	case *akita.SendStatsMsg:
		madeProgress = tlb.sendCollectedStatsToCP(now)
	default:
		log.Panicf("cannot process request %s", reflect.TypeOf(req))
	}
	if madeProgress {
		tlb.ControlPort.Retrieve(now)
		return true
	}
	return false
}

func (tlb *LastLevelTLB) visit(setID, wayID int) int {
	set := tlb.Sets[setID]
	mruPosition := set.Visit(wayID)
	return mruPosition
}

func (tlb *LastLevelTLB) handleTLBFlush(now akita.VTimeInSec, req *TLBFlushReq) bool {
	rsp := TLBFlushRspBuilder{}.
		WithSrc(tlb.ControlPort).
		WithDst(req.Src).
		WithSendTime(now).
		Build()

	err := tlb.ControlPort.Send(rsp)
	if err != nil {
		return false
	}

	for _, vAddr := range req.VAddr {
		// setID := tlb.vAddrToSetID(vAddr)
		setID := tlb.vAddrToSetIDxor7(vAddr)
		set := tlb.Sets[setID]
		wayID, page, found := set.Lookup(req.PID, vAddr)
		if !found {
			continue
		}

		page.Valid = false
		set.Update(wayID, page)
	}

	tlb.mshr.Reset()
	tlb.extensionmshr.Reset()
	tlb.isPaused = true
	return true
}

func (tlb *LastLevelTLB) handleTLBRestart(now akita.VTimeInSec, req *TLBRestartReq) bool {
	rsp := TLBRestartRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.ControlPort).
		WithDst(req.Src).
		Build()

	err := tlb.ControlPort.Send(rsp)
	if err != nil {
		return false
	}

	tlb.isPaused = false

	for tlb.TopPort.Retrieve(now) != nil {
		tlb.TopPort.Retrieve(now)
	}

	for tlb.BottomPort.Retrieve(now) != nil {
		tlb.BottomPort.Retrieve(now)
	}

	return true
}

func (tlb *LastLevelTLB) GetFrontQueueLength() int {
	bufContainer := tlb.GetTopPort().(akita.MsgBufferContainer) //.(*akita.LimitNumMsgPort)
	buf := bufContainer.GetBuffer()
	return len(buf)
}

func (tlb *LastLevelTLB) switchIndexing(now akita.VTimeInSec,
	req *akita.TLBIndexingSwitchMsg) bool {

	tlb.stats.numAccess = 0
	tlb.stats.lastChecked = 0
	tlb.stats.hitsInPrevEpoch = 0
	tlb.stats.missesInPrevEpoch = 0
	tlb.stats.accessesInPrevEpoch = 0
	tlb.stats.missesReturnedInPrevEpoch = 0
	tlb.stats.timesMeasuredInPrevEpoch = 0
	tlb.stats.avgQueueLengthInPrevEpoch = 0
	tlb.stats.numMSHRStallsInPrevEpoch = 0
	tlb.stats.avgMSHRLenInPrevEpoch = 0
	tlb.stats.numStalledInPrevEpoch = 0

	tlb.stats.hitsInCurEpoch = 0
	tlb.stats.missesInCurEpoch = 0
	tlb.stats.missesReturnedInCurEpoch = 0
	tlb.stats.accessesInCurEpoch = 0
	tlb.stats.timesMeasuredInCurEpoch = 0
	tlb.stats.avgQueueLengthInCurEpoch = 0
	tlb.stats.numMSHRStallsInCurEpoch = 0
	tlb.stats.avgMSHRLenInCurEpoch = 0
	tlb.stats.numStalledInCurEpoch = 0

	tlb.stats.missesReturned = 0
	if req.TLBInterleaving == 12 {
		tlb.stats.sendStateInfo = false
	} else {
		tlb.stats.sendStateInfo = true
	}
	tlb.stats.interleaving = req.TLBInterleaving

	HashFunc4KXOR := func(address uint64) uint64 {
		NumBitsPerTerm := 2
		NumTerms := 4
		index := uint64(0)
		mask := (uint64(1) << NumBitsPerTerm) - 1
		for i := 0; i < NumTerms; i++ {
			index = index ^ (address & mask)
			address = address >> NumBitsPerTerm
		}
		return index
	}
	// some parameters hardcoded
	HashFuncHSL := func(address uint64) uint64 {
		return (address / uint64(req.TLBInterleaving)) % 4
	}

	lmf := tlb.TLBFinder.(*cache.CustomTwoLevelLowModuleFinder)
	if req.TLBIndexingSwitch == akita.TLBIndexingSwitch4K {
		tlb.mode4Kstrip = true
		lmf.Hashfunc = HashFunc4KXOR
		fmt.Println(tlb.Name(), "L2 TLB ", tlb.Name(), "switching to 4K sir", now, req.TLBInterleaving)
	} else {
		tlb.mode4Kstrip = false
		lmf.Hashfunc = HashFuncHSL
		fmt.Println(tlb.Name(), "L2 TLB ", tlb.Name(), "switching to HSL sir", now, req.TLBInterleaving)
	}
	return true
}

func (tlb *LastLevelTLB) sendCollectedStatsToCP(now akita.VTimeInSec) bool {

	// TODO: set hits and misses appropriately
	req := akita.CollectedStatsMsgBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.ControlPort).
		WithDst(tlb.CommandProcessor).
		WithNumMisses(tlb.stats.missesInCurEpoch + tlb.stats.missesInPrevEpoch).
		WithNumHits(tlb.stats.hitsInCurEpoch + tlb.stats.hitsInPrevEpoch).
		Build()
	err := tlb.ControlPort.Send(req)
	if err != nil {
		panic("could not send stats to CP!")
	}
	return true
}

func (tlb *LastLevelTLB) doStatsCollection(now akita.VTimeInSec) bool {
	currentQueueLength := float64(tlb.GetFrontQueueLength())
	tlb.stats.avgQueueLengthInCurEpoch = (float64(tlb.stats.timesMeasuredInCurEpoch)*tlb.stats.avgQueueLengthInCurEpoch + currentQueueLength) / float64(tlb.stats.timesMeasuredInCurEpoch+1)
	if tlb.stats.accessesInCurEpoch > 5000 {
		tlb.stats.accessesInPrevEpoch = tlb.stats.accessesInCurEpoch
		tlb.stats.hitsInPrevEpoch = tlb.stats.hitsInCurEpoch
		tlb.stats.missesInPrevEpoch = tlb.stats.missesInCurEpoch
		tlb.stats.missesReturnedInPrevEpoch = tlb.stats.missesReturnedInCurEpoch
		tlb.stats.avgQueueLengthInPrevEpoch = tlb.stats.avgQueueLengthInCurEpoch
		tlb.stats.timesMeasuredInPrevEpoch = tlb.stats.timesMeasuredInCurEpoch
		tlb.stats.numMSHRStallsInPrevEpoch = tlb.stats.numMSHRStallsInCurEpoch
		tlb.stats.avgMSHRLenInPrevEpoch = tlb.stats.avgMSHRLenInCurEpoch
		tlb.stats.numStalledInPrevEpoch = tlb.stats.numStalledInCurEpoch
		tlb.stats.hitsInCurEpoch = 0
		tlb.stats.accessesInCurEpoch = 0
		tlb.stats.missesInCurEpoch = 0
		tlb.stats.missesReturnedInCurEpoch = 0
		tlb.stats.avgQueueLengthInCurEpoch = 0
		tlb.stats.timesMeasuredInCurEpoch = 0
		tlb.stats.numMSHRStallsInCurEpoch = 0
		tlb.stats.avgMSHRLenInCurEpoch = 0
		tlb.stats.numStalledInCurEpoch = 0
	}
	return true
}

// SetTLBFinder sets the TLBFinder of the LastLevelTLB
func (tlb *LastLevelTLB) SetTLBFinder(lmf cache.LowModuleFinder) {
	tlb.TLBFinder = lmf
}

func (tlb *LastLevelTLB) GetName() string {
	return tlb.Name()
}

func (tlb *LastLevelTLB) CheckTopPort(port akita.Port) bool {
	return port == tlb.TopPort
}

func (tlb *LastLevelTLB) CheckBottomPort(port akita.Port) bool {
	return port == tlb.BottomPort
}

package tlb

import (
	// "fmt"
	"fmt"
	"log"
	"reflect"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/tlb/internal"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

type TLB interface {
	tracing.NamedHookable

	GetNumSets() int
	GetNumWays() int
	GetTopPort() akita.Port
	GetBottomPort() akita.Port
	GetControlPort() akita.Port
	SetLowModuleFinder(cache.LowModuleFinder)
	GetLowModuleFinder() cache.LowModuleFinder
	GetPipeline() pipelining.Pipeline
	GetFrontQueueLength() int
	SetCommandProcessor(akita.Port)
	SetTLBFinder(cache.LowModuleFinder)
	SetGPCID(gpcID int)
}

// A TLB is a cache that maintains some page information.
type TLBImpl struct {
	*akita.TickingComponent

	TopPort     akita.Port
	BottomPort  akita.Port
	ControlPort akita.Port

	LowModule       akita.Port
	LowModuleFinder cache.LowModuleFinder

	numSets        int
	numWays        int
	log2PageSize   uint64
	log2NumWays    uint64
	log2NumSets    uint64
	pageSize       uint64
	numReqPerCycle int
	latency        int

	Sets []internal.Set

	pipeline     pipelining.Pipeline
	lookupBuffer util.Buffer

	mshr                  mshr
	respondingMSHREntries []*mshrEntry

	isPaused bool

	GlobalIndex int

	gpcID int
}

// GetNumSets gets the number of sets in the TLB
func (tlb *TLBImpl) GetNumSets() int {
	return tlb.numSets
}

// GetNumWays gets the number of ways in the TLB
func (tlb *TLBImpl) GetNumWays() int {
	return tlb.numWays
}

func (tlb *TLBImpl) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	tlb.LowModuleFinder = lmf
}

func (tlb *TLBImpl) GetLowModuleFinder() cache.LowModuleFinder {
	return tlb.LowModuleFinder
}

func (tlb *TLBImpl) GetTopPort() akita.Port {
	return tlb.TopPort
}

func (tlb *TLBImpl) GetBottomPort() akita.Port {
	return tlb.BottomPort
}

func (tlb *TLBImpl) GetControlPort() akita.Port {
	return tlb.ControlPort
}

func (tlb *TLBImpl) GetPipeline() pipelining.Pipeline {
	panic("not implemented")
}

func (tlb *TLBImpl) GetFrontQueueLength() int {
	panic("not implemented")
}

func (tlb *TLBImpl) SetCommandProcessor(port akita.Port) {
	panic("not implemented")
}

func (tlb *TLBImpl) SetTLBFinder(lmf cache.LowModuleFinder) {
	panic("not implemented")
}

func (tlb *TLBImpl) SetGPCID(id int) {
	tlb.gpcID = id
}

// Reset sets all the entries int he TLB to be invalid
func (tlb *TLBImpl) reset() {
	tlb.Sets = make([]internal.Set, tlb.numSets)
	for i := 0; i < tlb.numSets; i++ {
		set := internal.NewMultiLevelSet(tlb.numWays, tlb.log2PageSize)
		tlb.Sets[i] = set
	}
}

// Tick defines how TLB update states at each cycle
func (tlb *TLBImpl) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = tlb.performCtrlReq(now) || madeProgress

	if !tlb.isPaused {
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.respondMSHREntry(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.lookup(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseBottom(now) || madeProgress
		}

		madeProgress = tlb.pipeline.Tick(now) || madeProgress

		// pipeline or queue cycling done here

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseFromTop(now) || madeProgress
		}
	}

	return madeProgress
}

func (tlb *TLBImpl) respondMSHREntry(now akita.VTimeInSec) bool {
	if len(tlb.respondingMSHREntries) == 0 {
		return false
	}

	mshrEntry := tlb.respondingMSHREntries[0]
	page := mshrEntry.page
	req := mshrEntry.Requests[0]

	var accessResult device.AccessResult
	if mshrEntry.NumResponded() == 0 {
		accessResult = device.TLBMiss
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
		//device.TLBMiss).
		Build()
	err := tlb.TopPort.Send(rspToTop)
	if err != nil {
		return false
	}
	mshrEntry.IncNumRespondedByOne()
	mshrEntry.Requests = mshrEntry.Requests[1:]
	if len(mshrEntry.Requests) == 0 {
		tlb.respondingMSHREntries = tlb.respondingMSHREntries[1:]
	}
	tracing.StartTracingNetworkReq(rspToTop, now, tlb, req)
	tracing.TraceReqComplete(req, now, tlb)
	return true
}

func (tlb *TLBImpl) lookup(now akita.VTimeInSec) bool {
	item := tlb.lookupBuffer.Peek()
	if item == nil {
		return false
	}
	pipelineItem := item.(tlbPipelineItem)
	req := pipelineItem.translationReq

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
			return true
		}
		return false
	}

	setID := tlb.vAddrToSetIDMultiLevel(req.VAddr)
	set := tlb.Sets[setID]
	wayID, page, found := set.Lookup(req.PID, req.VAddr)
	if found && page.Valid {
		return tlb.handleTranslationHit(now, req, setID, wayID, page)
	}

	return tlb.handleTranslationMiss(now, req)
}

func (tlb *TLBImpl) handleTranslationHit(
	now akita.VTimeInSec,
	req *device.TranslationReq,
	setID, wayID int,
	page device.Page,
) bool {
	ok := tlb.sendRspToTop(now, req, page)
	if !ok {
		return false
	}

	// mruPos := tlb.visit(setID, wayID)
	tlb.visit(setID, wayID)
	tlb.lookupBuffer.Pop()

	tracing.AddTaskDetailedStep(
		tracing.MsgIDAtReceiver(req, tlb),
		now, tlb,
		"tlb-hit",
		req.VAddr,
	)
	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(req, tlb),
		now, tlb,
		"tlb-hit-size-"+fmt.Sprint(page.SizeBits),
	)
	// tracing.AddTaskStep(
	// 	tracing.MsgIDAtReceiver(req, tlb),
	// 	now, tlb,
	// 	fmt.Sprintf("tlb-hit-at-set-%d-mru-pos-%d", setID, mruPos),
	// )
	// s := fmt.Sprintf("tlb-hit-at-set-%d-mru-pos-%d\n", setID, mruPos)
	// fmt.Println(s)
	tracing.TraceReqComplete(req, now, tlb)

	return true
}

func (tlb *TLBImpl) handleTranslationMiss(
	now akita.VTimeInSec,
	req *device.TranslationReq,
) bool {
	// if tlb.shouldDoPageWalk(req.VAddr) {
	if tlb.mshr.IsFull() {
		return false
	}
	fetched := tlb.fetchBottom(now, req)
	if fetched {
		tlb.lookupBuffer.Pop()
		tracing.AddTaskDetailedStep(
			tracing.MsgIDAtReceiver(req, tlb),
			now, tlb,
			"tlb-miss",
			req.VAddr,
		)
		return true
	}
	return false
}

func (tlb *TLBImpl) sendRspToTop(
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
		Build()

	err := tlb.TopPort.Send(rsp)
	if err == nil {
		tracing.StartTracingNetworkReq(rsp, now, tlb, req)
		return true
	}
	return false
}

func (tlb *TLBImpl) processTLBMSHRHit(
	now akita.VTimeInSec,
	mshrEntry *mshrEntry,
	req *device.TranslationReq,
) bool {
	// if len(mshrEntry.Requests) < 8 {
	// 	mshrEntry.Requests = append(mshrEntry.Requests, req)
	// 	return true
	// }
	// return false
	mshrEntry.Requests = append(mshrEntry.Requests, req)

	return true
	/*	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(req /*mshrEntry.Requests[0], tlb),
		now, tlb,
		"tlb-mshr-hit",
	) */
}

func (tlb *TLBImpl) fetchBottom(now akita.VTimeInSec, req *device.TranslationReq) bool {
	dstPort := tlb.LowModuleFinder.Find(req.VAddr)

	fetchBottom := device.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.BottomPort).
		// WithDst(tlb.LowModule).
		WithDst(dstPort).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithDeviceID(req.DeviceID).
		WithTLBID(tlb.GlobalIndex).
		WithGPCID(tlb.gpcID).
		Build()
	err := tlb.BottomPort.Send(fetchBottom)
	if err != nil {
		return false
	}

	mshrEntry := tlb.mshr.Add(req.PID, req.VAddr)
	mshrEntry.Requests = append(mshrEntry.Requests, req)
	mshrEntry.reqToBottom = fetchBottom

	tracing.StartTask(
		fetchBottom.Meta().ID+"_L2TLB_stats",
		tracing.MsgIDAtReceiver(req, tlb),
		now,
		tlb,
		"L2TLB_stats",
		reflect.TypeOf(req).String(),
		req,
	)
	tracing.StartTracingNetworkTLBReq(fetchBottom, now, tlb, req, tlb.gpcID)
	// tracing.TraceReqReceive(req, now, tlb)
	tracing.TraceReqInitiate(fetchBottom, now, tlb,
		tracing.MsgIDAtReceiver(req, tlb))

	return true
}

func getTaskStep(origin string, accessResult device.AccessResult) (step string) {
	step = origin + "-"
	switch accessResult {
	case device.TLBHit:
		step += "TLBHit"
	case device.TLBMiss:
		step += "TLBMiss"
	case device.TLBMshrHit:
		step += "TLBMshrHit"
	}
	return
}

func (tlb *TLBImpl) parseFromTop(now akita.VTimeInSec) bool {
	msg := tlb.TopPort.Peek()
	if msg == nil {
		return false
	}

	req := msg.(*device.TranslationReq)

	// push to waiting queue
	if tlb.pipeline.CanAccept() {
		pipelineItem := tlbPipelineItem{
			taskID:         akita.GetIDGenerator().Generate(),
			translationReq: req,
		}
		tlb.pipeline.Accept(now, pipelineItem)
		tlb.TopPort.Retrieve(now)
		tracing.TraceReqReceive(req, now, tlb)
		tracing.StopTracingNetworkReq(req, now, tlb)
		return true
	}
	return false
}

func (tlb *TLBImpl) parseBottom(now akita.VTimeInSec) bool {
	if len(tlb.respondingMSHREntries) != 0 {
		return false
	}

	item := tlb.BottomPort.Peek()
	if item == nil {
		return false
	}

	rsp := item.(*device.TranslationRsp)
	page := rsp.Page
	//	fmt.Println(rsp.Meta().ID)//, req.Meta().ID)

	mshrEntryPresent := tlb.mshr.IsEntriesPresent(page)
	if !mshrEntryPresent {
		tlb.BottomPort.Retrieve(now)
		tracing.TraceReqFinalize(rsp, now, tlb)
		return true
	}

	setID := tlb.vAddrToSetIDMultiLevel(page.VAddr)
	set := tlb.Sets[setID]
	wayID, ok := tlb.Sets[setID].Evict()
	if !ok {
		panic("failed to evict")
	}
	set.Update(wayID, page)
	set.Visit(wayID)

	mshrEntries := tlb.mshr.GetEntries(page)
	tlb.respondingMSHREntries = mshrEntries
	for _, e := range mshrEntries {
		e.page = page
		if e.reqToBottom != nil {
			tracing.TraceReqFinalize(e.reqToBottom, now, tlb)
		}
	}
	tlb.mshr.RemoveEntries(mshrEntries)

	tlb.BottomPort.Retrieve(now)

	tracing.StopTracingNetworkReq(rsp, now, tlb)

	return true
}

func (tlb *TLBImpl) vAddrToSetID(vAddr uint64) (setID int) {
	return int(vAddr / tlb.pageSize % uint64(tlb.numSets))
}

func (tlb *TLBImpl) vAddrToSetIDMultiLevel(vAddr uint64) int {
	const log2MinPageGroupSize = uint64(16)

	log2DiscardBits := (log2MinPageGroupSize + tlb.log2NumWays) - tlb.log2PageSize

	vpn := vAddr >> tlb.log2PageSize
	indexedVpn := vpn >> uint(log2DiscardBits)

	return int(indexedVpn % uint64(tlb.numSets))
}

func (tlb *TLBImpl) performCtrlReq(now akita.VTimeInSec) bool {
	item := tlb.ControlPort.Peek()
	if item == nil {
		return false
	}

	item = tlb.ControlPort.Retrieve(now)

	switch req := item.(type) {
	case *TLBFlushReq:
		return tlb.handleTLBFlush(now, req)
	case *TLBRestartReq:
		return tlb.handleTLBRestart(now, req)
	case *akita.TLBIndexingSwitchMsg:
		return tlb.switchIndexing(now, req)
	default:
		log.Panicf("cannot process request %s", reflect.TypeOf(req))
	}

	return true
}

func (tlb *TLBImpl) visit(setID, wayID int) int {
	set := tlb.Sets[setID]
	mruPosition := set.Visit(wayID)
	return mruPosition
}

func (tlb *TLBImpl) handleTLBFlush(now akita.VTimeInSec, req *TLBFlushReq) bool {
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
		setID := tlb.vAddrToSetIDMultiLevel(vAddr)
		set := tlb.Sets[setID]
		wayID, page, found := set.Lookup(req.PID, vAddr)
		if !found {
			continue
		}

		page.Valid = false
		set.Update(wayID, page)
	}

	tlb.mshr.Reset()
	tlb.isPaused = true
	return true
}

func (tlb *TLBImpl) handleTLBRestart(now akita.VTimeInSec, req *TLBRestartReq) bool {
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

func (tlb *TLBImpl) switchIndexing(now akita.VTimeInSec,
	req *akita.TLBIndexingSwitchMsg) bool {
	lmf := tlb.LowModuleFinder.(*cache.CustomTwoLevelLowModuleFinder)

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

	if req.TLBIndexingSwitch == akita.TLBIndexingSwitch4K {
		lmf.Hashfunc = HashFunc4KXOR
	} else {
		lmf.Hashfunc = HashFuncHSL
	}

	fmt.Println(tlb.Name(), "L1 TLB ", tlb.Name(), "switching to ", req.TLBIndexingSwitch, " sir", now, req.TLBInterleaving)
	return true
}

func (tlb *TLBImpl) GetName() string {
	return tlb.Name()
}

func (tlb *TLBImpl) CheckTopPort(port akita.Port) bool {
	return port == tlb.TopPort
}

func (tlb *TLBImpl) CheckBottomPort(port akita.Port) bool {
	return port == tlb.BottomPort
}

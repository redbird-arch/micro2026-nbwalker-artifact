package tlb

import (
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

// A SMSideL1TLB is a cache that maintains some page information.
type SMSideL1TLB struct {
	*akita.TickingComponent

	TopPort     akita.Port
	RemotePort  akita.Port
	LocalPort   akita.Port
	ControlPort akita.Port

	LowModule       akita.Port
	LowModuleFinder *cache.PartitionedXORLowModuleFinder

	numSets        int
	numWays        int
	pageSize       uint64
	numReqPerCycle int
	latency        int

	Sets []internal.Set

	pipeline     pipelining.Pipeline
	lookupBuffer util.Buffer

	mshr                mshr
	respondingMSHREntry *mshrEntry

	isPaused bool

	GlobalIndex    int
	PartitionIndex uint64

	RRPtr bool

	gpcID int
}

// GetNumSets gets the number of sets in the TLB
func (tlb *SMSideL1TLB) GetNumSets() int {
	return tlb.numSets
}

// GetNumWays gets the number of ways in the TLB
func (tlb *SMSideL1TLB) GetNumWays() int {
	return tlb.numWays
}

func (tlb *SMSideL1TLB) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	panic("SMSideL1TLB only supports PartitionedXORLowModuleFinder")
}

func (tlb *SMSideL1TLB) SetPartitionedXORLowModuleFinder(lmf *cache.PartitionedXORLowModuleFinder) {
	tlb.LowModuleFinder = lmf
}

func (tlb *SMSideL1TLB) GetLowModuleFinder() cache.LowModuleFinder {
	return tlb.LowModuleFinder
}

func (tlb *SMSideL1TLB) GetTopPort() akita.Port {
	return tlb.TopPort
}

func (tlb *SMSideL1TLB) GetBottomPort() akita.Port {
	panic("SMSideL1TLB does not have BottomPort")
}

func (tlb *SMSideL1TLB) GetControlPort() akita.Port {
	return tlb.ControlPort
}

func (tlb *SMSideL1TLB) GetPipeline() pipelining.Pipeline {
	panic("not implemented")
}

func (tlb *SMSideL1TLB) GetFrontQueueLength() int {
	panic("not implemented")
}

func (tlb *SMSideL1TLB) SetCommandProcessor(port akita.Port) {
	panic("not implemented")
}

func (tlb *SMSideL1TLB) SetTLBFinder(lmf cache.LowModuleFinder) {
	panic("not implemented")
}

func (tlb *SMSideL1TLB) SetGPCID(gpcID int) {
	tlb.gpcID = gpcID
}

// Reset sets all the entries int he SMSideL1TLB to be invalid
func (tlb *SMSideL1TLB) reset() {
	tlb.Sets = make([]internal.Set, tlb.numSets)
	for i := 0; i < tlb.numSets; i++ {
		set := internal.NewSet(tlb.numWays)
		tlb.Sets[i] = set
	}
}

// Tick defines how SMSideL1TLB update states at each cycle
func (tlb *SMSideL1TLB) Tick(now akita.VTimeInSec) bool {
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

func (tlb *SMSideL1TLB) respondMSHREntry(now akita.VTimeInSec) bool {
	if tlb.respondingMSHREntry == nil {
		return false
	}

	mshrEntry := tlb.respondingMSHREntry
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
		tlb.respondingMSHREntry = nil
	}
	tracing.StartTracingNetworkReq(rspToTop, now, tlb, req)
	tracing.TraceReqComplete(req, now, tlb)
	return true
}

func (tlb *SMSideL1TLB) lookup(now akita.VTimeInSec) bool {
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
			tracing.AddTaskStep(
				tracing.MsgIDAtReceiver(req, tlb),
				now, tlb,
				"tlb-mshr-hit",
			)
			tlb.lookupBuffer.Pop()
			return true
		}
		return false
	}

	setID := tlb.vAddrToSetID(req.VAddr)
	set := tlb.Sets[setID]
	wayID, page, found := set.Lookup(req.PID, req.VAddr)
	if found && page.Valid {
		return tlb.handleTranslationHit(now, req, setID, wayID, page)
	}

	return tlb.handleTranslationMiss(now, req)
}

func (tlb *SMSideL1TLB) handleTranslationHit(
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

	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(req, tlb),
		now, tlb,
		"tlb-hit",
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

func (tlb *SMSideL1TLB) handleTranslationMiss(
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
		tracing.AddTaskStep(
			tracing.MsgIDAtReceiver(req, tlb),
			now, tlb,
			"tlb-miss",
		)
		return true
	}
	return false
}

func (tlb *SMSideL1TLB) vAddrToSetID(vAddr uint64) (setID int) {
	return int(vAddr / tlb.pageSize % uint64(tlb.numSets))
}

func (tlb *SMSideL1TLB) sendRspToTop(
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

func (tlb *SMSideL1TLB) processTLBMSHRHit(
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

func (tlb *SMSideL1TLB) fetchBottom(now akita.VTimeInSec, req *device.TranslationReq) bool {
	dstPort, local := tlb.LowModuleFinder.FindID(tlb.PartitionIndex, req.VAddr)

	bottomPort := tlb.RemotePort
	if local {
		bottomPort = tlb.LocalPort
	}

	fetchBottom := device.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(bottomPort).
		WithDst(dstPort).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithDeviceID(req.DeviceID).
		WithTLBID(tlb.GlobalIndex).
		WithPartitionID(int(tlb.PartitionIndex)).
		Build()
	err := bottomPort.Send(fetchBottom)
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
	tracing.StartTracingNetworkReq(fetchBottom, now, tlb, req)
	// tracing.TraceReqReceive(req, now, tlb)
	tracing.TraceReqInitiate(fetchBottom, now, tlb,
		tracing.MsgIDAtReceiver(req, tlb))
	if local {
		tracing.StartTask(
			fetchBottom.Meta().ID+"_req_out",
			tracing.MsgIDAtReceiver(req, tlb),
			now,
			tlb,
			"local_req_out",
			reflect.TypeOf(fetchBottom).String(),
			tlb,
		)
	} else {
		tracing.StartTask(
			fetchBottom.Meta().ID+"_req_out",
			tracing.MsgIDAtReceiver(req, tlb),
			now,
			tlb,
			"remote_req_out",
			reflect.TypeOf(fetchBottom).String(),
			tlb,
		)
	}

	return true
}

func (tlb *SMSideL1TLB) parseFromTop(now akita.VTimeInSec) bool {
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

func (tlb *SMSideL1TLB) parseBottom(now akita.VTimeInSec) bool {
	if tlb.respondingMSHREntry != nil {
		return false
	}

	var bottomPort akita.Port
	if tlb.RRPtr {
		bottomPort = tlb.RemotePort
	} else {
		bottomPort = tlb.LocalPort
	}

	tlb.RRPtr = !tlb.RRPtr

	item := bottomPort.Peek()
	if item == nil {
		return false
	}

	rsp := item.(*device.TranslationRsp)
	page := rsp.Page
	//	fmt.Println(rsp.Meta().ID)//, req.Meta().ID)

	mshrEntryPresent := tlb.mshr.IsEntryPresent(rsp.Page.PID, rsp.Page.VAddr)
	if !mshrEntryPresent {
		panic("oh no!")
		bottomPort.Retrieve(now)
		tracing.TraceReqFinalize(rsp, now, tlb)
		return true
	}

	setID := tlb.vAddrToSetID(page.VAddr)
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
	bottomPort.Retrieve(now)

	tracing.StopTracingNetworkReq(rsp, now, tlb)
	tracing.TraceReqFinalize(mshrEntry.reqToBottom, now, tlb)

	_, local := tlb.LowModuleFinder.FindID(
		tlb.PartitionIndex, mshrEntry.reqToBottom.VAddr)

	if local {
		tracing.EndTask(
			mshrEntry.reqToBottom.Meta().ID+"local_req_out",
			now,
			tlb,
		)
	} else {
		tracing.EndTask(
			mshrEntry.reqToBottom.Meta().ID+"remote_req_out",
			now,
			tlb,
		)
	}

	tracing.EndTask(
		mshrEntry.reqToBottom.Meta().ID+"_L2TLB_stats",
		now,
		tlb,
	)

	return true
}

func (tlb *SMSideL1TLB) performCtrlReq(now akita.VTimeInSec) bool {
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

func (tlb *SMSideL1TLB) visit(setID, wayID int) int {
	set := tlb.Sets[setID]
	mruPosition := set.Visit(wayID)
	return mruPosition
}

func (tlb *SMSideL1TLB) handleTLBFlush(now akita.VTimeInSec, req *TLBFlushReq) bool {
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
		setID := tlb.vAddrToSetID(vAddr)
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

func (tlb *SMSideL1TLB) handleTLBRestart(now akita.VTimeInSec, req *TLBRestartReq) bool {
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

	for tlb.LocalPort.Retrieve(now) != nil {
		tlb.LocalPort.Retrieve(now)
	}

	for tlb.RemotePort.Retrieve(now) != nil {
		tlb.RemotePort.Retrieve(now)
	}

	return true
}

func (tlb *SMSideL1TLB) switchIndexing(now akita.VTimeInSec,
	req *akita.TLBIndexingSwitchMsg) bool {
	panic("not implemented")
}

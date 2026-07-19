package idealtlb

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

type IdealTLB struct {
	*akita.TickingComponent

	TopPort     akita.Port
	BottomPort  akita.Port
	ControlPort akita.Port

	pageTable device.PageTable

	numReqPerCycle int

	isPaused bool

	gpcID int
}

func (tlb *IdealTLB) GetNumSets() int {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetNumWays() int {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetTopPort() akita.Port {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetBottomPort() akita.Port {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetControlPort() akita.Port {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) SetLowModuleFinder(finder cache.LowModuleFinder) {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetLowModuleFinder() cache.LowModuleFinder {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetPipeline() pipelining.Pipeline {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) GetFrontQueueLength() int {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) SetCommandProcessor(port akita.Port) {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) SetTLBFinder(finder cache.LowModuleFinder) {
	//TODO implement me
	panic("implement me")
}

func (tlb *IdealTLB) SetGPCID(id int) {
	tlb.gpcID = id
}

// Tick defines how TLB update states at each cycle
func (tlb *IdealTLB) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	if !tlb.isPaused {
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.lookup(now) || madeProgress
		}
	}

	return madeProgress
}

func (tlb *IdealTLB) lookup(now akita.VTimeInSec) bool {
	msg := tlb.TopPort.Peek()
	if msg == nil {
		return false
	}

	req := msg.(*device.TranslationReq)

	// get the page magically
	page, found := tlb.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("Physical to Virtual address translation not found!!")
	}

	return tlb.handleTranslationHit(now, req, page)
}

func (tlb *IdealTLB) handleTranslationHit(
	now akita.VTimeInSec,
	req *device.TranslationReq,
	page device.Page,
) bool {
	ok := tlb.sendRspToTop(now, req, page)
	if !ok {
		return false
	}

	tlb.TopPort.Retrieve(now)

	tracing.StopTracingNetworkReq(req, now, tlb)

	tracing.TraceReqReceive(req, now, tlb)
	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(req, tlb),
		now, tlb,
		"tlb-hit",
	)
	tracing.TraceReqComplete(req, now, tlb)

	return true
}

func (tlb *IdealTLB) sendRspToTop(
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
		Build()
	err := tlb.TopPort.Send(rsp)
	if err == nil {
		tracing.StartTracingNetworkReq(rsp, now, tlb, req)
		return true
	}

	return false
}

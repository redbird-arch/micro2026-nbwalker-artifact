package mmu

import (
	"fmt"
	"log"
	"reflect"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

type IdealMMUPipelineItem struct {
	taskID  string
	request *device.TranslationReq
}

func (t IdealMMUPipelineItem) TaskID() string {
	return t.taskID
}

// IdealMMU is the default mmu implementation. It is also an akita Component.
type IdealMMU struct {
	akita.TickingComponent

	ToTop            akita.Port
	ControlPort      akita.Port
	CommandProcessor akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder

	pageTable *device.PageTableImpl

	remoteMemAccessesInCurEpoch  uint64
	avgWalksEnqueuedInCurEpoch   float64
	remoteMemAccessesInPrevEpoch uint64
	avgWalksEnqueuedInPrevEpoch  float64
	memAccessesInCurEpoch        uint64
	memAccessesInPrevEpoch       uint64

	numWalksDone        uint64
	numWalksInCurEpoch  uint64
	numWalksInPrevEpoch uint64
	lastChecked         uint64
	sendStateInfo       bool
	// numWalksArrived          uint64
	interleaving uint64

	pipeline     pipelining.Pipeline
	lookupBuffer util.Buffer

	numActiveTransactions uint64
	maxActiveTransactions uint64
}

func (mmu *IdealMMU) GetNumActiveWalkers() int {
	return int(mmu.numActiveTransactions)
}

func (mmu *IdealMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *IdealMMU) ToTranslationPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *IdealMMU) ToPageWalkCachePort() akita.Port {
	return mmu.ToPageWalkCache
}

func (mmu *IdealMMU) ToCachePort() akita.Port {
	return nil
}

func (mmu *IdealMMU) CommandProcessorPort() akita.Port {
	return mmu.CommandProcessor
}

func (mmu *IdealMMU) SetCommandProcessorPort(port akita.Port) {
	mmu.CommandProcessor = port
}

func (mmu *IdealMMU) ControlPortPort() akita.Port {
	return mmu.ControlPort
}

// Tick defines how the MMU update state each cycle
func (mmu *IdealMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.performCtrlReq(now) || madeProgress

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress
	madeProgress = mmu.parseFromBuffer(now) || madeProgress
	madeProgress = mmu.pipeline.Tick(now) || madeProgress

	tracing.StartTask("", "", now, mmu, "num_active_walkers", fmt.Sprintf("%d", mmu.numActiveTransactions), nil)

	return madeProgress
}

func (mmu *IdealMMU) performCtrlReq(now akita.VTimeInSec) bool {
	item := mmu.ControlPort.Peek()
	if item == nil {
		return false
	}
	madeProgress := false
	switch req := item.(type) {
	case *akita.TLBIndexingSwitchMsg:
		madeProgress = mmu.switchIndexing(now, req)
	case *akita.SendStatsMsg:
		madeProgress = mmu.sendCollectedStatsToCP(now)
	default:
		log.Panicf("cannot process request %s", reflect.TypeOf(req))
	}
	if madeProgress {
		mmu.ControlPort.Retrieve(now)
		return true
	}
	panic("something is wrong!")
}

func (mmu *IdealMMU) sendCollectedStatsToCP(now akita.VTimeInSec) bool {
	req := akita.CollectedStatsMsgBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ControlPort).
		WithDst(mmu.CommandProcessor).
		Build()
	err := mmu.ControlPort.Send(req)
	if err != nil {
		panic("could not send stats to CP!")
	}
	mmu.ControlPort.Retrieve(now)
	return true
}

func (mmu *IdealMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mmu,
		Now:    now,
		Item:   what,
	}

	mmu.InvokeHook(ctx)
}

func (mmu *IdealMMU) sendMsgToCP(now akita.VTimeInSec) bool {
	if !mmu.sendStateInfo {
		panic("how are we sending a message to CP when we shouldn't be?!")
	}
	req := akita.SendStatsMsgBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ControlPort).
		WithDst(mmu.CommandProcessor).
		Build()

	err := mmu.ControlPort.Send(req)
	if err != nil {
		fmt.Println(err)
		// panic("oh no")
	}
	return true
}

func (mmu *IdealMMU) switchIndexing(now akita.VTimeInSec,
	req *akita.TLBIndexingSwitchMsg) bool {

	mmu.numWalksDone = 0
	mmu.lastChecked = 0

	mmu.avgWalksEnqueuedInCurEpoch = 0
	mmu.avgWalksEnqueuedInPrevEpoch = 0

	mmu.remoteMemAccessesInCurEpoch = 0
	mmu.remoteMemAccessesInPrevEpoch = 0

	mmu.memAccessesInCurEpoch = 0
	mmu.memAccessesInPrevEpoch = 0

	mmu.numWalksInCurEpoch = 0
	mmu.numWalksInPrevEpoch = 0

	if req.TLBInterleaving == 12 {
		mmu.sendStateInfo = false
	} else {
		mmu.sendStateInfo = true
	}
	mmu.interleaving = req.TLBInterleaving
	// fmt.Println(mmu.Name(), "flushing stats on switch", now, req.TLBInterleaving)
	return true
}

func (mmu *IdealMMU) parseFromBuffer(now akita.VTimeInSec) bool {
	madeProgress := false

	for {
		item := mmu.lookupBuffer.Peek()

		if item == nil {
			return madeProgress
		}

		req := item.(IdealMMUPipelineItem).request

		result := mmu.completeTranslation(req, now)

		if !result {
			return madeProgress
		}

		madeProgress = true
	}
}

func (mmu *IdealMMU) completeTranslation(req *device.TranslationReq, now akita.VTimeInSec) bool {
	if !mmu.topSender.CanSend(1) {
		return false
	}

	page, ok := mmu.pageTable.Find(req.PID, req.VAddr)
	if !ok {
		log.Panicf("cannot find page for vaddr 0x%x", req.VAddr)
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.ToTop).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		Build()

	mmu.topSender.Send(rsp)

	tracing.TraceReqComplete(req, now, mmu)

	mmu.lookupBuffer.Pop()

	mmu.numActiveTransactions--

	return true
}

func (mmu *IdealMMU) parseFromTop(now akita.VTimeInSec) bool {
	madeProgress := false

	for {
		req := mmu.ToTop.Peek()
		if req == nil {
			return madeProgress
		}

		if !mmu.CanAccept() {
			return madeProgress
		}

		switch req := req.(type) {
		case *device.TranslationReq:
			result := mmu.startWalking(req, now)

			if !result {
				return madeProgress
			}

			madeProgress = true
		default:
			log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
		}
	}
}

func (mmu *IdealMMU) startWalking(req *device.TranslationReq, now akita.VTimeInSec) bool {
	if mmu.pipeline.CanAccept() {
		pipelineItem := IdealMMUPipelineItem{
			taskID:  akita.GetIDGenerator().Generate(),
			request: req,
		}

		mmu.pipeline.Accept(now, pipelineItem)

		tracing.TraceReqReceive(req, now, mmu)

		mmu.ToTop.Retrieve(now)

		mmu.numActiveTransactions++

		return true
	}

	return false
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *IdealMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *IdealMMU) CanAccept() bool {
	if mmu.maxActiveTransactions == 0 {
		return true
	}

	return mmu.numActiveTransactions < mmu.maxActiveTransactions
}

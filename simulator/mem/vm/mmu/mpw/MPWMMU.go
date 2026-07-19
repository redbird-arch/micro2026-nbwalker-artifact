package mpw

import (
	"encoding/binary"
	"log"
	"reflect"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/ca"
	"gitlab.com/akita/util/tracing"
)

type transactionState int

const (
	newTransaction transactionState = iota
	pageWalkCacheDone
	batchMemReqs
	sentToMem
	memDone
	transactionFinished
)

type transactionImpl struct {
	akita.MsgMeta

	req               *device.TranslationReq
	memReq            *mem.ReadReq
	page              device.Page
	level             int
	msgID             string
	state             transactionState
	Address           uint64
	PPN               uint64
	vAddr             uint64
	remoteMemAccesses int
	pid               ca.PID
}

func (r *transactionImpl) TaskID() string {
	return r.msgID
}

func (r *transactionImpl) Meta() *akita.MsgMeta {
	return &r.MsgMeta
}

type MPWWalkerStatus struct {
	state         transactionState
	requestVector map[int]struct{}
}

type MPWPageWalker struct {
	status *MPWWalkerStatus
	queue  []*transactionImpl

	batchedTransactions []*transactionImpl
}

// MPWMMU is the default mmu implementation. It is also an akita Component.
type MPWMMU struct {
	akita.TickingComponent

	ToTop akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort   akita.Port
	translationSender akitaext.BufferedSender
	lowModuleFinder   cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers   []*MPWPageWalker
	nextPointer   int
	queueCapacity int
}

// Tick defines how the MMU update state each cycle
func (mpw *MPWMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mpw.topSender.Tick(now) || madeProgress
	madeProgress = mpw.translationSender.Tick(now) || madeProgress
	madeProgress = mpw.walkPageTable(now) || madeProgress
	madeProgress = mpw.parseFromPageWalkCache(now) || madeProgress
	madeProgress = mpw.parseFromMem(now) || madeProgress
	madeProgress = mpw.parseFromTop(now) || madeProgress

	return true
}

func (mpw *MPWMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: mpw,
		Now:    now,
		Item:   what,
	}

	mpw.InvokeHook(ctx)
}

func (mpw *MPWMMU) walkPageTable(now akita.VTimeInSec) bool {
	madeProgress := false

	numInflightPTWRequests := 0
	pageWalkQueueLen := 0
	for _, walker := range mpw.pageWalkers {
		if len(walker.queue) == 0 {
			continue
		}

		status := walker.status

		switch status.state {
		case pageWalkCacheDone:
			mpw.generateMemReqs(walker)
		case batchMemReqs:
			mpw.sendToMem(now, walker)
		case memDone:
			mpw.generateMemReqs(walker)
		case transactionFinished:
			mpw.removeFromWalker(walker)
		}

		numInflightPTWRequests += len(walker.status.requestVector)
		pageWalkQueueLen += len(walker.queue)

		madeProgress = true
	}

	if mpw.isActive() {
		tracing.StartTask(
			"",
			"",
			now,
			mpw,
			"num_active_walkers",
			strconv.Itoa(numInflightPTWRequests),
			nil,
		)
		tracing.StartTask(
			"",
			"",
			now,
			mpw,
			"page_walk_queue_len",
			strconv.Itoa(pageWalkQueueLen),
			nil,
		)
	}

	return madeProgress
}

func (mpw *MPWMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := mpw.ToPageWalkCache.Peek()
	if item == nil {
		return false
	}

	switch item.(type) {
	case *mem.WriteDoneRsp:
		mpw.ToPageWalkCache.Retrieve(now)

		return true
	default:
		panic("unknown message type")
	}

	return false
}

func (mpw *MPWMMU) parseFromMem(now akita.VTimeInSec) bool {
	madeProgress := false
	item := mpw.TranslationPort.Peek()
	if item != nil {
		switch msg := item.(type) {
		case *mem.DataReadyRsp:
			mpw.handleMemResponse(msg, now)
		default:
			panic("unknown message type")
		}
		madeProgress = true
	}
	mpw.TranslationPort.Retrieve(now)
	return madeProgress
}

func (mpw *MPWMMU) generateMemReqs(walker *MPWPageWalker) {
	transState := walker.status.state
	if transState != pageWalkCacheDone && transState != memDone {
		panic("this state shouldn't be here!")
	}

	if len(walker.status.requestVector) != 0 {
		panic("there are still requests in flight!")
	}

	if len(walker.batchedTransactions) != 0 {
		panic("there are still batched transactions!")
	}

	batchedLevel := 0
	requestVector := make(map[int]struct{})
	// scan the queue
	for i := 0; i < len(walker.queue); i++ {
		if walker.queue[i] == nil {
			continue
		}

		trans := walker.queue[i]

		if len(walker.batchedTransactions) == 0 {
			batchedLevel = trans.level
		}

		if trans.level != batchedLevel {
			continue
		}

		walker.batchedTransactions = append(
			walker.batchedTransactions, trans)

		requestVector[i] = struct{}{}
	}

	walker.status.state = batchMemReqs
	walker.status.requestVector = requestVector
}

func (mpw *MPWMMU) sendToMem(now akita.VTimeInSec, walker *MPWPageWalker) {
	transState := walker.status.state
	if transState != batchMemReqs {
		panic("this state shouldn't be here!")
	}

	if !mpw.translationSender.CanSend(1) {
		return
	}

	trans := walker.batchedTransactions[0]

	PPN := trans.PPN
	PPNWithOffset := mpw.pageTable.AddOffset(PPN, trans.vAddr)

	srcPort := mpw.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := mpw.lowModuleFinder.Find(PPNWithOffset)

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(srcPort).
		WithDst(dstPort).
		WithPID(trans.req.PID).
		WithAddress(PPNWithOffset).
		WithByteSize(8).
		WithInfo(readReqInfo).
		Build()

	readReq.PTW = true

	mpw.translationSender.Send(readReq)

	trans.vAddr = mpw.pageTable.NextLevel(trans.vAddr)
	trans.msgID = readReq.ID
	trans.state = sentToMem

	partitionID := mmu.ExtractMPID(dstPort.Name())

	if partitionID < 4 {
		tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mpw),
			now, mpw, "page_walk_req_left")
	} else {
		tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mpw),
			now, mpw, "page_walk_req_right")
	}

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, mpw),
		now, mpw, "page_walk_req_local")

	tracing.StartTask(
		readReq.ID,
		"",
		now,
		mpw,
		"walker_mem_latency",
		reflect.TypeOf(readReq).String(),
		readReq,
	)

	walker.batchedTransactions = walker.batchedTransactions[1:]

	if len(walker.batchedTransactions) == 0 {
		walker.status.state = sentToMem
	}
}

func (mpw *MPWMMU) removeFromWalker(walker *MPWPageWalker) {
	tmp := walker.queue[:0]
	for _, trans := range walker.queue {
		if trans != nil && trans.state != transactionFinished {
			tmp = append(tmp, trans)
		}
	}

	walker.queue = tmp

	if len(walker.queue) == 0 {
		walker.status.state = newTransaction
	} else {
		walker.status.state = pageWalkCacheDone
	}
}

func (mpw *MPWMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for _, walker := range mpw.pageWalkers {
		if len(walker.queue) == 0 {
			continue
		}

		for j := range walker.queue {
			if walker.queue[j] == nil {
				continue
			}

			if _, exist := walker.status.requestVector[j]; !exist {
				continue
			}

			trans := walker.queue[j]

			if trans.msgID != rsp.RespondTo {
				continue
			}

			trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
			trans.state = memDone
			if trans.level+1 == 4 {
				mpw.finalizeTransaction(now, trans)
			} else {
				mpw.fillPageWalkCache(now, trans)
			}
			trans.level++

			tracing.EndTask(rsp.RespondTo, now, mpw)

			delete(walker.status.requestVector, j)

			if len(walker.status.requestVector) == 0 {
				if trans.state == transactionFinished {
					walker.status.state = transactionFinished
				} else {
					walker.status.state = memDone
				}
			}

			return
		}
	}
	panic("cannot handle mem response")
}

func (mpw *MPWMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *transactionImpl,
) bool {
	level := uint64(trans.level)
	data := mmu.Uint64ToBytes(trans.PPN | level)
	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(mpw.ToPageWalkCache).
		WithDst(mpw.PageWalkCache).
		WithPID(trans.req.PID).
		WithAddress(mpw.pageTable.AlignToPage(trans.req.VAddr) | level).
		WithData(data).
		Build()

	err := mpw.ToPageWalkCache.Send(writeReq)
	if err != nil {
		return false
	}

	trans.msgID = writeReq.ID

	return true
}

func (mpw *MPWMMU) finalizeTransaction(
	now akita.VTimeInSec,
	trans *transactionImpl,
) bool {
	req := trans.req

	page, found := mpw.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("page not found")
	}

	pAddr := trans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{PID: req.PID, VAddr: req.VAddr, PAddr: pAddr, Valid: true}
	trans.page = newPage
	trans.state = transactionFinished

	return mpw.doPageWalkHit(now, trans)
}

func (mpw *MPWMMU) doPageWalkHit(
	now akita.VTimeInSec,
	trans *transactionImpl,
) bool {
	if !mpw.topSender.CanSend(1) {
		return false
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mpw.ToTop).
		WithDst(trans.req.Src).
		WithRspTo(trans.req.ID).
		WithPage(trans.page).
		Build()

	mpw.topSender.Send(rsp)

	tracing.TraceReqComplete(trans.req, now, mpw)

	return true
}

func (mpw *MPWMMU) parseFromTop(now akita.VTimeInSec) bool {
	item := mpw.ToTop.Peek()
	if item == nil {
		return false
	}

	req, ok := item.(*device.TranslationReq)
	if !ok {
		log.Panicf("MMU cannot handle request of type %s", reflect.TypeOf(req))
	}

	for i := 0; i < len(mpw.pageWalkers); i++ {
		index := (mpw.nextPointer + i) % len(mpw.pageWalkers)
		if len(mpw.pageWalkers[index].queue) < mpw.queueCapacity {
			rearrangedVAddr := mpw.pageTable.Rearrange(req.VAddr)
			root := mpw.pageTable.GetRoot(req.PID)
			translationInPipeline := transactionImpl{
				req:   req,
				level: 0,
				msgID: "invalid",
				state: pageWalkCacheDone,
				vAddr: rearrangedVAddr,
				PPN:   root,
			}

			if req.Data != nil {
				rspData := binary.LittleEndian.Uint64(req.Data)
				translationInPipeline.PPN = rspData & ^uint64(3)
				level := int(rspData & uint64(3))
				translationInPipeline.vAddr = mpw.pageTable.MoveToLevel(
					translationInPipeline.vAddr,
					level+1,
				)
				translationInPipeline.level = level + 1
			}

			tracing.AddTaskStep(tracing.MsgIDAtReceiver(translationInPipeline.req, mpw),
				now, mpw, "pwc-hit-level"+strconv.Itoa(translationInPipeline.level))

			mpw.pageWalkers[index].queue = append(
				mpw.pageWalkers[index].queue,
				&translationInPipeline,
			)
			mpw.nextPointer = (index + 1) % len(mpw.pageWalkers)

			mpw.ToTop.Retrieve(now)

			tracing.StartTask(
				tracing.MsgIDAtReceiver(req, mpw),
				req.Meta().ID,
				now,
				mpw,
				"req",
				reflect.TypeOf(req).String(),
				req,
			)

			tracing.TraceReqReceive(req, now, mpw)

			if mpw.pageWalkers[index].status.state == newTransaction {
				mpw.pageWalkers[index].status.state = pageWalkCacheDone
			}
			return true
		}
	}

	return false
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mpw *MPWMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mpw.lowModuleFinder = lmf
}

func (mpw *MPWMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range mpw.pageWalkers {
		num += len(mpw.pageWalkers[i].status.requestVector)
	}
	return num
}

func (mpw *MPWMMU) ToTopPort() akita.Port {
	return mpw.ToTop
}

func (mpw *MPWMMU) ToTranslationPort() akita.Port {
	return mpw.TranslationPort
}

func (mpw *MPWMMU) ToCachePort() akita.Port {
	return nil
}

func (mpw *MPWMMU) CanAccept() bool {
	for i := range mpw.pageWalkers {
		if len(mpw.pageWalkers[i].queue) < mpw.queueCapacity {
			return true
		}
	}

	return false
}

func (mpw *MPWMMU) ToPageWalkCachePort() akita.Port {
	return mpw.ToPageWalkCache
}

func (mpw *MPWMMU) isActive() bool {
	for i := range mpw.pageWalkers {
		if len(mpw.pageWalkers[i].queue) > 0 {
			return true
		}
	}

	return false
}

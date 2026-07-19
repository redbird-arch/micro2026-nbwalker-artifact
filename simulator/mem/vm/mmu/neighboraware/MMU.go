package neighboraware

import (
	"encoding/binary"
	"log"
	"reflect"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/ca"
	"gitlab.com/akita/util/tracing"
)

type transactionState int

const (
	pageWalkCacheDone transactionState = iota
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

func (t *transactionImpl) TaskID() string {
	return t.msgID
}

func (t *transactionImpl) Meta() *akita.MsgMeta {
	return &t.MsgMeta
}

func (t *transactionImpl) GetPPN() uint64 {
	return t.PPN
}

func (t *transactionImpl) GetMemReq() *mem.ReadReq {
	return t.memReq
}

type PageWalkerImpl struct {
	inflightTrans *transactionImpl
}

// NeighBorMMU is the default mmu implementation. It is also an akita Component.
type NeighBorMMU struct {
	akita.TickingComponent

	ToTop akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort akita.Port
	lowModuleFinder cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers   []PageWalkerImpl
	nextPointer   int
	queueCapacity int

	queue []*transactionImpl

	monitorStats *monitor.CaPWQMonitorStats
}

func (m *NeighBorMMU) SentCommand(info interface{}) {
	//TODO implement me
	panic("implement me")
}

func (impl *NeighBorMMU) InitMonitorStats() {
	impl.monitorStats = &monitor.CaPWQMonitorStats{}
}

func (impl *NeighBorMMU) ClearMonitorStats() {
	impl.monitorStats.Clear()
}

func (impl *NeighBorMMU) GetMonitorStats() interface{} {
	return impl.monitorStats
}

// Tick defines how the MMU update state each cycle
func (impl *NeighBorMMU) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = impl.topSender.Tick(now) || madeProgress
	madeProgress = impl.scanInQueueTrans(now) || madeProgress
	madeProgress = impl.walkPageTable(now) || madeProgress
	madeProgress = impl.parseFromPageWalkCache(now) || madeProgress
	madeProgress = impl.parseFromMem(now) || madeProgress
	madeProgress = impl.parseFromTop(now) || madeProgress

	if impl.monitorStats != nil {
		pageWalkQueueLength := len(impl.queue)
		impl.monitorStats.ReqLength = uint64(pageWalkQueueLength)
	}

	return true
}

func (impl *NeighBorMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: impl,
		Now:    now,
		Item:   what,
	}

	impl.InvokeHook(ctx)
}

func (impl *NeighBorMMU) walkPageTable(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := range impl.pageWalkers {
		inflightTrans := impl.pageWalkers[i].inflightTrans
		if inflightTrans == nil {
			continue
		}

		switch inflightTrans.state {
		case pageWalkCacheDone:
			impl.sendToMem(now, inflightTrans)
		case memDone:
			impl.sendToMem(now, inflightTrans)
		case transactionFinished:
			impl.pageWalkers[i].inflightTrans = nil
		}

		madeProgress = true
	}

	return madeProgress
}

func (impl *NeighBorMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
	item := impl.ToPageWalkCache.Peek()
	if item == nil {
		return false
	}

	switch item.(type) {
	case *mem.WriteDoneRsp:
		impl.ToPageWalkCache.Retrieve(now)

		return true
	default:
		panic("unknown message type")
	}

	return false
}

func (impl *NeighBorMMU) parseFromMem(now akita.VTimeInSec) bool {
	madeProgress := false
	item := impl.TranslationPort.Peek()
	if item != nil {
		switch msg := item.(type) {
		case *mem.DataReadyRsp:
			impl.handleMemResponse(msg, now)
		default:
			panic("unknown message type")
		}
		madeProgress = true
	}
	impl.TranslationPort.Retrieve(now)
	return madeProgress
}

func (impl *NeighBorMMU) sendToMem(now akita.VTimeInSec, trans *transactionImpl) {
	transState := trans.state
	if transState != pageWalkCacheDone && transState != memDone {
		panic("this state shouldn't be here!")
	}

	PPN := trans.PPN
	PPNWithOffset := impl.pageTable.AddOffset(PPN, trans.vAddr)

	srcPort := impl.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := impl.lowModuleFinder.Find(PPNWithOffset)

	cachelineID := PPNWithOffset >> 6 << 6

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(srcPort).
		WithDst(dstPort).
		WithPID(trans.req.PID).
		WithAddress(cachelineID).
		WithByteSize(64).
		WithInfo(readReqInfo).
		Build()

	readReq.PTW = true

	err := srcPort.Send(readReq)
	if err != nil {
		return
	}

	trans.vAddr = impl.pageTable.NextLevel(trans.vAddr)
	trans.msgID = readReq.ID
	trans.state = sentToMem
	trans.memReq = readReq // For tracing

	partitionID := mmu.ExtractMPID(dstPort.Name())

	if partitionID < 4 {
		tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, impl),
			now, impl, "page_walk_req_left")
	} else {
		tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, impl),
			now, impl, "page_walk_req_right")
	}

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(trans.req, impl),
		now, impl, "page_walk_req_local")

	tracing.StartTask(
		readReq.ID,
		"",
		now,
		impl,
		"walker_mem_latency",
		reflect.TypeOf(readReq).String(),
		readReq,
	)
}

// extractPTEIndex 从虚拟地址中提取对应层级的 PTE 索引偏移（0-7）
// 注意：此版本 level 1=Root, 2=L3, 3=L2, 4=Leaf
func (impl *NeighBorMMU) extractPTEIndex(vAddr uint64, level int) uint64 {
	var shift uint64

	// 1. 根据你的定义重新映射位移量 [cite: 200-205]
	switch level {
	case 0: // Root (对应传统的 L4)
		shift = 39 // Bits [47:39]
	case 1: // L3
		shift = 30 // Bits [38:30]
	case 2: // L2
		shift = 21 // Bits [29:21]
	case 3: // Leaf (对应传统的 L1)
		shift = 12 // Bits [20:12]
	default:
		return 0
	}

	// 2. 提取该层级的 9 位 Index [cite: 195, 291]
	// Index = (VA >> shift) & 511
	fullIndex := (vAddr >> shift) & 0x1FF

	// 3. 提取缓存行内偏移 (8个条目，取低3位) [cite: 291, 334]
	return fullIndex & 0x7
}

func (impl *NeighBorMMU) populatePWQ(
	vAddress uint64,
	data []byte,
	level int,
) {
	for _, trans := range impl.queue {
		if trans == nil {
			continue
		}

		if impl.isInsideNeighborhood(vAddress, trans.req.VAddr, level) {

			// 3. 计算该请求对应的 PTE 在 64 字节缓存行中的偏移 [cite: 411]
			// 每个 PTE 8 字节，通过 VA 的索引位确定它是 8 个中的哪一个
			pteIndex := impl.extractPTEIndex(trans.req.VAddr, level)
			startByte := pteIndex * 8
			pteData := binary.LittleEndian.Uint64(data[startByte : startByte+8])

			trans.PPN = pteData
			trans.level = level + 1
			trans.state = memDone

			trans.vAddr = impl.pageTable.MoveFromVAddrToLevel(
				trans.req.VAddr,
				level,
			)
		}
	}
}

func (impl *NeighBorMMU) isInsideNeighborhood(va1, va2 uint64, level int) bool {
	var neighborhoodShift uint64
	switch level {
	case 0:
		neighborhoodShift = 42 // 4TB 邻域 (Root级合并)
	case 1:
		neighborhoodShift = 33 // 8GB 邻域
	case 2:
		neighborhoodShift = 24 // 16MB 邻域
	case 3:
		neighborhoodShift = 15 // 32KB 邻域 (Leaf级完全合并)
	}
	// 比较高位地址是否一致 [cite: 392, 394]
	return (va1 >> neighborhoodShift) == (va2 >> neighborhoodShift)
}

func (impl *NeighBorMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for i := range impl.pageWalkers {
		if impl.pageWalkers[i].inflightTrans == nil {
			continue
		}

		trans := impl.pageWalkers[i].inflightTrans
		if trans.msgID == rsp.RespondTo {
			pteIndex := impl.extractPTEIndex(trans.req.VAddr, trans.level)
			startByte := pteIndex * 8
			pteData := binary.LittleEndian.Uint64(rsp.Data[startByte : startByte+8])

			trans.PPN = pteData
			trans.state = memDone

			tracing.AddTaskStepWithDetail(
				"",
				now,
				impl,
				"ptw-mem-req",
				trans,
			)

			if trans.level+1 == 4 {
				impl.finalizeTransaction(now, i)
			} else {
				impl.fillPageWalkCache(now, i)
			}

			impl.populatePWQ(trans.req.VAddr, rsp.Data, trans.level)

			trans.level++

			tracing.EndTask(rsp.RespondTo, now, impl)
		}
	}
}

func (impl *NeighBorMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	i int,
) bool {
	trans := impl.pageWalkers[i].inflightTrans

	if trans == nil {
		panic("no inflight transaction")
	}

	level := uint64(trans.level)
	data := mmu.Uint64ToBytes(trans.PPN | level)

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(impl.ToPageWalkCache).
		WithDst(impl.PageWalkCache).
		WithPID(trans.req.PID).
		WithAddress(impl.pageTable.AlignToPage(trans.req.VAddr) | level).
		WithData(data).
		Build()

	err := impl.ToPageWalkCache.Send(writeReq)
	if err != nil {
		return false
	}

	trans.msgID = writeReq.ID

	return true
}

func (impl *NeighBorMMU) scanInQueueTrans(
	now akita.VTimeInSec,
) bool {
	madeProgress := false

	for _, trans := range impl.queue {
		if trans == nil {
			continue
		}

		if trans.level < 4 {
			continue
		}

		if trans.state == transactionFinished {
			continue
		}

		page, found := impl.pageTable.Find(trans.req.PID, trans.req.VAddr)
		if !found {
			panic("page not found")
		}

		pAddr := trans.PPN
		if pAddr != page.PAddr {
			panic("addresses don't match!")
		}

		if !impl.topSender.CanSend(1) {
			return madeProgress
		}

		newPage := device.Page{PID: trans.req.PID, VAddr: trans.req.VAddr, PAddr: pAddr, Valid: true}

		trans.page = newPage
		trans.state = transactionFinished

		madeProgress = madeProgress || impl.doTransPageWalkHit(now, trans)
	}

	return madeProgress
}

func (impl *NeighBorMMU) finalizeTransaction(
	now akita.VTimeInSec,
	walkingIndex int,
) bool {
	req := impl.pageWalkers[walkingIndex].inflightTrans.req

	page, found := impl.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("page not found")
	}

	pAddr := impl.pageWalkers[walkingIndex].inflightTrans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{PID: req.PID, VAddr: req.VAddr, PAddr: pAddr, Valid: true}

	impl.pageWalkers[walkingIndex].inflightTrans.page = newPage
	impl.pageWalkers[walkingIndex].inflightTrans.state = transactionFinished

	return impl.doPageWalkHit(now, walkingIndex)
}

func (impl *NeighBorMMU) doTransPageWalkHit(
	now akita.VTimeInSec,
	trans *transactionImpl,
) bool {
	if !impl.topSender.CanSend(1) {
		panic("should be able to send when doing page walk hit")
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(impl.ToTop).
		WithDst(trans.req.Src).
		WithRspTo(trans.req.ID).
		WithPage(trans.page).
		Build()

	impl.topSender.Send(rsp)

	tracing.TraceReqComplete(trans.req, now, impl)

	return true
}

func (impl *NeighBorMMU) doPageWalkHit(
	now akita.VTimeInSec,
	walkingIndex int,
) bool {
	if !impl.topSender.CanSend(1) {
		return false
	}

	walking := impl.pageWalkers[walkingIndex].inflightTrans

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(impl.ToTop).
		WithDst(walking.req.Src).
		WithRspTo(walking.req.ID).
		WithPage(walking.page).
		Build()

	impl.topSender.Send(rsp)

	tracing.TraceReqComplete(walking.req, now, impl)

	return true
}

func (impl *NeighBorMMU) parseFromTop(now akita.VTimeInSec) bool {
	madeProgress := false

	item := impl.ToTop.Peek()

	if item != nil {
		req, ok := item.(*device.TranslationReq)
		if !ok {
			log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
		}

		if len(impl.queue) < impl.queueCapacity {
			impl.queue = append(impl.queue, &transactionImpl{req: req})

			impl.ToTop.Retrieve(now)

			tracing.StartTask(
				tracing.MsgIDAtReceiver(req, impl),
				req.Meta().ID,
				now,
				impl,
				"req",
				reflect.TypeOf(req).String(),
				req,
			)

			tracing.TraceReqReceive(req, now, impl)

			madeProgress = true
		}
	}

	for i := 0; i < len(impl.pageWalkers); i++ {
		if len(impl.queue) == 0 {
			return madeProgress
		}

		index := (impl.nextPointer + i) % len(impl.pageWalkers)

		walker := &impl.pageWalkers[index]

		if walker.inflightTrans == nil {
			req := impl.queue[0].req

			rearrangedVAddr := impl.pageTable.Rearrange(req.VAddr)
			root := impl.pageTable.GetRoot(req.PID)
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
				translationInPipeline.vAddr = impl.pageTable.MoveToLevel(
					translationInPipeline.vAddr,
					level+1,
				)
				translationInPipeline.level = level + 1
			}

			tracing.AddTaskStep(tracing.MsgIDAtReceiver(translationInPipeline.req, impl),
				now, impl, "pwc-hit-level"+strconv.Itoa(translationInPipeline.level))

			walker.inflightTrans = &translationInPipeline

			impl.queue = impl.queue[1:]

			impl.nextPointer = (index + 1) % len(impl.pageWalkers)

			madeProgress = true
		}
	}

	return madeProgress
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *NeighBorMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *NeighBorMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans != nil {
			num++
		}
	}
	return num
}

func (mmu *NeighBorMMU) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *NeighBorMMU) ToTranslationPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *NeighBorMMU) ToCachePort() akita.Port {
	return nil
}

func (mmu *NeighBorMMU) CanAccept() bool {
	if len(mmu.queue) < mmu.queueCapacity {
		return true
	}

	return false
}

func (mmu *NeighBorMMU) ToPageWalkCachePort() akita.Port {
	return mmu.ToPageWalkCache
}

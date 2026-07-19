package baseline

import (
	"bytes"
	"encoding/binary"
	"log"
	"reflect"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/cache/writeback"
	"gitlab.com/akita/mem/cpu"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/idealmemcontroller"
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
	queue         []*device.TranslationReq
	inflightTrans *transactionImpl
}

// MMUImpl is the default mmu implementation. It is also an akita Component.
type MMUImpl struct {
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

	monitorStats *monitor.CaPWQMonitorStats

	CPU *cpu.CPUStorage

	Drams               []*idealmemcontroller.Comp
	DramLowModuleFinder *cache.StripedLocalVRemoteLowModuleFinder
	L2Caches            []*writeback.Cache
}

func (m *MMUImpl) SentCommand(info interface{}) {
	//TODO implement me
	panic("implement me")
}

func (impl *MMUImpl) InitMonitorStats() {
	impl.monitorStats = &monitor.CaPWQMonitorStats{}
}

func (impl *MMUImpl) ClearMonitorStats() {
	impl.monitorStats.Clear()
}

func (impl *MMUImpl) GetMonitorStats() interface{} {
	return impl.monitorStats
}

// Tick defines how the MMU update state each cycle
func (impl *MMUImpl) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = impl.topSender.Tick(now) || madeProgress
	madeProgress = impl.walkPageTable(now) || madeProgress
	madeProgress = impl.parseFromPageWalkCache(now) || madeProgress
	madeProgress = impl.parseFromMem(now) || madeProgress
	madeProgress = impl.parseFromTop(now) || madeProgress

	if impl.monitorStats != nil {
		pageWalkQueueLength := 0
		for _, walker := range impl.pageWalkers {
			pageWalkQueueLength += len(walker.queue)
		}
		impl.monitorStats.ReqLength = uint64(pageWalkQueueLength)
	}

	if impl.isActive() {
		tracing.StartTask(
			"",
			"",
			now,
			impl,
			"num_active_walkers",
			strconv.Itoa(impl.GetNumActiveWalkers()),
			nil,
		)
	}

	return true
}

func (impl *MMUImpl) isActive() bool {
	for i := range impl.pageWalkers {
		if impl.pageWalkers[i].inflightTrans != nil {
			return true
		}
	}

	return false
}

func (impl *MMUImpl) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: impl,
		Now:    now,
		Item:   what,
	}

	impl.InvokeHook(ctx)
}

func (impl *MMUImpl) walkPageTable(now akita.VTimeInSec) bool {
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

func (impl *MMUImpl) parseFromPageWalkCache(now akita.VTimeInSec) bool {
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

func (impl *MMUImpl) parseFromMem(now akita.VTimeInSec) bool {
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

func (impl *MMUImpl) sendToMem(now akita.VTimeInSec, trans *transactionImpl) {
	transState := trans.state
	if transState != pageWalkCacheDone && transState != memDone {
		panic("this state shouldn't be here!")
	}

	PPN := trans.PPN
	PPNWithOffset := impl.pageTable.AddOffset(PPN, trans.vAddr)

	srcPort := impl.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := impl.lowModuleFinder.Find(PPNWithOffset)

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

func (impl *MMUImpl) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for i := range impl.pageWalkers {
		if impl.pageWalkers[i].inflightTrans == nil {
			continue
		}

		trans := impl.pageWalkers[i].inflightTrans
		if trans.msgID == rsp.RespondTo {
			trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
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
			trans.level++

			tracing.EndTask(rsp.RespondTo, now, impl)
		}
	}
}

func (impl *MMUImpl) fillPageWalkCache(
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

func (impl *MMUImpl) finalizeTransaction(
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

func (impl *MMUImpl) doPageWalkHit(
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

func (impl *MMUImpl) parseFromTop(now akita.VTimeInSec) bool {
	madeProgress := false

	item := impl.ToTop.Peek()

	if item != nil {
		req, ok := item.(*device.TranslationReq)
		if !ok {
			log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
		}

		impl.checkDemandPaging(req)

		for i := 0; i < len(impl.pageWalkers); i++ {
			index := (impl.nextPointer + i) % len(impl.pageWalkers)
			if len(impl.pageWalkers[index].queue) < impl.queueCapacity {
				impl.pageWalkers[index].queue = append(impl.pageWalkers[index].queue, req)
				impl.nextPointer = (index + 1) % len(impl.pageWalkers)

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

				break
			}
		}
	}

	for i := range impl.pageWalkers {
		walker := &impl.pageWalkers[i]

		if walker.inflightTrans == nil && len(walker.queue) > 0 {
			req := walker.queue[0]

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

			walker.queue = walker.queue[1:]

			madeProgress = true
		}
	}

	return madeProgress
}

// SetLowModuleFinder sets the table recording where to find an address.
func (mmu *MMUImpl) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	mmu.lowModuleFinder = lmf
}

func (mmu *MMUImpl) GetNumActiveWalkers() int {
	num := 0
	for i := range mmu.pageWalkers {
		if mmu.pageWalkers[i].inflightTrans != nil {
			num++
		}
	}
	return num
}

func (mmu *MMUImpl) ToTopPort() akita.Port {
	return mmu.ToTop
}

func (mmu *MMUImpl) ToTranslationPort() akita.Port {
	return mmu.TranslationPort
}

func (mmu *MMUImpl) ToCachePort() akita.Port {
	return nil
}

func (mmu *MMUImpl) CanAccept() bool {
	for i := range mmu.pageWalkers {
		if len(mmu.pageWalkers[i].queue) < mmu.queueCapacity {
			return true
		}
	}

	return false
}

func (mmu *MMUImpl) ToPageWalkCachePort() akita.Port {
	return mmu.ToPageWalkCache
}

func (impl *MMUImpl) checkDemandPaging(
	req *device.TranslationReq,
) {
	// Demand Paging
	page, ok := impl.pageTable.Find(req.PID, req.VAddr)
	if !ok {
		log.Panicf("not found: PID %d, VAddr %x", req.PID, req.VAddr)
	}

	if !page.Valid {
		if page.Unified {
			newPages := impl.pageTable.AllocateMultiplePages(
				req.PID,
				1,
				page.VAddr,
				16,
				true,
			)

			newVAddrs := make([]uint64, 0)

			cachelineSet := make(map[uint64]struct{})
			cachelines := make([]uint64, 0)
			for _, page := range newPages {
				newVAddrs = append(newVAddrs, page.VAddr)

				cacheline := page.PAddr & ^uint64(63)

				if _, found := cachelineSet[cacheline]; !found {
					cachelineSet[cacheline] = struct{}{}
					cachelines = append(cachelines, cacheline)
				}
			}

			for _, cacheline := range cachelines {
				for _, cache := range impl.L2Caches {
					res := cache.Invalidate(req.PID, cacheline)

					if res {
						log.Printf("Invalidated cacheline %x for PID %d in cache %s due to demand paging",
							cacheline, req.PID, cache.Name())
					}
				}
			}

			impl.writePageTablePage(req.PID)
			impl.writeMultiplePages(page.PID, page.VAddr)

			impl.pageTable.ProcessNewAllocations(req.PID, newVAddrs)

			tracing.AddTaskStep(
				"",
				0,
				impl,
				"page_fault",
			)
		} else {
			log.Panicf("invalid page: PID %d, VAddr %x", req.PID, req.VAddr)
		}
	} else {
		if page.PAddr == 0 {
			log.Panicf("invalid page address: PID %d, VAddr %x", req.PID, req.VAddr)
		}
	}
}

func (impl *MMUImpl) writePageTablePage(
	pid ca.PID,
) {
	pageTablePages := impl.pageTable.PageTablePagesAsBytes(pid)

	for len(pageTablePages) > 0 {
		pAddr := pageTablePages[0]
		children := pageTablePages[1:513]
		pageTablePages = pageTablePages[513:]
		buffer := bytes.NewBuffer(nil)
		err := binary.Write(buffer, binary.LittleEndian, children)
		if err != nil {
			panic(err)
		}
		rawBytes := buffer.Bytes()

		impl.writeToDRAM(
			pAddr,
			rawBytes,
		)
	}
}

func (impl *MMUImpl) writeMultiplePages(
	pid ca.PID,
	addr uint64,
) {
	chunkSize := uint64(16) * impl.pageTable.PageSize()
	vAddr := addr - (addr % chunkSize)

	for i := uint64(0); i < 16; i++ {
		pageVAddr := vAddr + i*impl.pageTable.PageSize()

		if !impl.CPU.CheckAddr(pageVAddr) {
			continue
		}

		data := impl.CPU.Read(pageVAddr)
		if len(data) != int(impl.pageTable.PageSize()) {
			log.Panicf("data size %d does not match page size %d", len(data), impl.pageTable.PageSize())
		}

		page, found := impl.pageTable.Find(pid, pageVAddr)
		if !found {
			log.Panicf("page not found for vaddr %x", pageVAddr)
		}

		impl.writeToDRAM(
			page.PAddr,
			data,
		)
	}
}

func (mmu *MMUImpl) writeToDRAM(
	pAddr uint64,
	data []byte,
) {
	offset := uint64(0)
	lengthLeft := uint64(len(data))
	addr := pAddr

	log2AccessSize := uint64(6)

	for lengthLeft > 0 {
		addrUnitFirstByte := addr & (^uint64(0) << log2AccessSize)
		unitOffset := addr - addrUnitFirstByte
		lengthInUnit := (1 << log2AccessSize) - unitOffset

		length := lengthLeft
		if lengthInUnit < length {
			length = lengthInUnit
		}

		dramIndex := mmu.DramLowModuleFinder.Index(pAddr)
		if dramIndex < 0 || dramIndex >= uint64(len(mmu.Drams)) {
			log.Panicf("cannot find dram for address %x", pAddr)
		}
		module := mmu.Drams[dramIndex]

		err := module.DirectWrite(addr, data[offset:offset+length])
		if err != nil {
			panic(err)
		}

		addr += length
		lengthLeft -= length
		offset += length
	}
}

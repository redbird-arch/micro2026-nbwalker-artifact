package NBWalkerMMU

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/monitor"
	"gitlab.com/akita/mem/vm"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/util/akitaext"
	"gitlab.com/akita/util/ca"
	"gitlab.com/akita/util/tracing"
)

type transactionState int

const (
	pageWalkCacheDone transactionState = iota
	sentToMem
	l1Done
	memDone
	transactionFinished
)

type transactionImpl struct {
	akita.MsgMeta

	req     *device.TranslationReq
	level   int
	msgID   string
	state   transactionState
	Address uint64
	PPN     uint64
	vAddr   uint64
	pid     ca.PID
	data    []byte
}

type CaPWQPageWalker struct {
	*akita.TickingComponent

	mmu                  *NBWalkerMMU
	transaction          *transactionImpl
	secondaryTransaction *transactionImpl
	info                 uint64
}

func newCaPWQPageWalker(mmu *NBWalkerMMU, id int) *CaPWQPageWalker {
	walker := &CaPWQPageWalker{
		mmu: mmu,
	}

	walker.TickingComponent = akita.NewTickingComponent(
		fmt.Sprintf("%s.CaPWQPageWalker_%02d", mmu.Name(), id),
		mmu.Engine,
		mmu.Freq,
		walker,
	)

	return walker
}

func (walker *CaPWQPageWalker) Tick(now akita.VTimeInSec) bool {
	if walker.transaction == nil && walker.secondaryTransaction == nil {
		return false
	}

	return walker.walkPageTable(now)
}

func (walker *CaPWQPageWalker) CanAccept() bool {
	return walker.transaction == nil || walker.secondaryTransaction == nil
}

func (walker *CaPWQPageWalker) CanAcceptPrimary() bool {
	return walker.transaction == nil
}

func (walker *CaPWQPageWalker) AcceptReqFromTop(
	now akita.VTimeInSec,
	req *device.TranslationReq,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	// Initialize a transaction
	rearrangedVAddr := walker.mmu.pageTable.Rearrange(req.VAddr)
	root := walker.mmu.pageTable.GetRoot(req.PID)

	transaction := &transactionImpl{
		state:   pageWalkCacheDone,
		Address: req.VAddr,
		pid:     req.PID,
		msgID:   akita.GetIDGenerator().Generate(),
		PPN:     root,
		vAddr:   rearrangedVAddr,
		level:   0,
	}

	if req.Data != nil {
		rspData := binary.LittleEndian.Uint64(req.Data)
		transaction.PPN = rspData & ^uint64(3)
		level := int(rspData & uint64(3))
		transaction.vAddr = walker.mmu.pageTable.MoveToLevel(
			transaction.vAddr,
			level+1,
		)
		transaction.level = level + 1
	}

	if walker.transaction == nil {
		walker.transaction = transaction
		walker.transaction.req = req
	} else {
		walker.secondaryTransaction = transaction
	}

	tracing.AddTaskStep("",
		now, walker.mmu, "pwc-hit-level"+strconv.Itoa(transaction.level))

	tracing.StartTask(
		transaction.msgID,
		"",
		now,
		walker.mmu,
		"req_in",
		"",
		nil,
	)

	walker.TickLater(now)
}

func (walker *CaPWQPageWalker) AcceptL1CacheRsp(
	now akita.VTimeInSec,
	newTrans *transactionImpl,
) {
	if !walker.CanAccept() {
		panic("walker can't accept")
	}

	if walker.transaction == nil {
		walker.transaction = newTrans
	} else {
		walker.secondaryTransaction = newTrans
	}

	walker.TickLater(now)
}

func (walker *CaPWQPageWalker) AcceptMemRsp(
	now akita.VTimeInSec,
	rsp *mem.DataReadyRsp,
) {
	if walker.transaction == nil {
		panic("walker has no primary transaction")
	}

	trans := walker.transaction

	trans.PPN = binary.LittleEndian.Uint64(rsp.Data)
	trans.state = memDone

	tracing.AddTaskStepWithDetail(
		"",
		now,
		walker.mmu,
		"ptw-mem-req",
		trans,
	)

	if trans.level+1 == 4 {
		walker.mmu.finalizeTransaction(now, trans)
	} else {
		walker.mmu.fillPageWalkCache(now, trans)
	}
	trans.level++

	tracing.EndTask(rsp.RespondTo, now, walker.mmu)

	walker.TickLater(now)
}

func (walker *CaPWQPageWalker) walkPageTable(now akita.VTimeInSec) bool {
	if walker.transaction == nil || walker.transaction.state == sentToMem {
		return walker.walkSecondaryPageTable(now)
	}

	switch walker.transaction.state {
	case pageWalkCacheDone, memDone, l1Done:
		walker.sendToMem(now)
	case transactionFinished:
		walker.transaction = nil
	default:
		panic("invalid transaction state")
	}

	return true
}

func (walker *CaPWQPageWalker) walkSecondaryPageTable(now akita.VTimeInSec) bool {
	if walker.secondaryTransaction == nil {
		return false
	}

	switch walker.secondaryTransaction.state {
	case pageWalkCacheDone, l1Done:
		walker.sendWriteReqToL1V(now)
	case transactionFinished:
		walker.secondaryTransaction = nil
	default:
		panic("invalid transaction state")
	}

	return true
}

func (walker *CaPWQPageWalker) sendToMem(now akita.VTimeInSec) {
	trans := walker.transaction

	transState := trans.state
	if transState != pageWalkCacheDone &&
		transState != memDone && transState != l1Done {
		panic("this state shouldn't be here!")
	}

	if trans.state == l1Done {
		trans.vAddr = walker.mmu.pageTable.MoveFromVAddrToLevel(
			trans.Address,
			trans.level,
		)
	}

	PPN := trans.PPN
	PPNWithOffset := walker.mmu.pageTable.AddOffset(PPN, trans.vAddr)

	srcPort := walker.mmu.TranslationPort
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	dstPort := walker.mmu.lowModuleFinder.Find(PPNWithOffset)

	readReq := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(srcPort).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(PPNWithOffset).
		WithByteSize(8).
		WithInfo(readReqInfo).
		Build()

	readReq.PTW = true

	err := srcPort.Send(readReq)
	if err != nil {
		return
	}

	trans.vAddr = walker.mmu.pageTable.NextLevel(trans.vAddr)
	trans.msgID = readReq.ID
	trans.state = sentToMem

	partitionID := mmu.ExtractMPID(dstPort.Name())

	if partitionID < 4 {
		tracing.AddTaskStep("",
			now, walker.mmu, "page_walk_req_left")
	} else {
		tracing.AddTaskStep("",
			now, walker.mmu, "page_walk_req_right")
	}

	tracing.AddTaskStep("",
		now, walker.mmu, "page_walk_req_local")

	tracing.StartTask(
		readReq.ID,
		"",
		now,
		walker.mmu,
		"walker_mem_latency",
		reflect.TypeOf(readReq).String(),
		readReq,
	)
}

func (walker *CaPWQPageWalker) sendWriteReqToL1V(now akita.VTimeInSec) {
	trans := walker.secondaryTransaction

	if trans.state != pageWalkCacheDone && trans.state != l1Done {
		panic("this state shouldn't be here!")
	}

	if trans.state == l1Done {
		trans.vAddr = walker.mmu.pageTable.MoveFromVAddrToLevel(
			trans.Address,
			trans.level,
		)
	}

	PPN := trans.PPN
	PPNWithOffset := walker.mmu.pageTable.AddOffset(PPN, trans.vAddr)

	// dstPort := walker.mmu.VCacheLowModuleFinder.Find(PPNWithOffset)
	lowModules := walker.mmu.VCacheLowModuleFinder.(*cache.XORLowModuleFinder).LowModules
	dstPort := lowModules[walker.mmu.vRR%uint64(len(lowModules))]

	block := vm.NBWalkerBlock{
		PID:           trans.pid,
		Address:       trans.Address,
		PPNWithOffset: PPNWithOffset,
		Level:         trans.level,
		MsgID:         trans.msgID,
	}

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(walker.mmu.ToCache).
		WithDst(dstPort).
		WithPID(trans.pid).
		WithAddress(PPNWithOffset).
		WithInfo(block).
		Build()

	writeReq.TrafficBytes += 12
	writeReq.PTW = true

	err := walker.mmu.ToCache.Send(writeReq)
	if err != nil {
		tracing.StartTask(walker.mmu.Name()+"stall", "", now, walker.mmu, "mmu_stall", "", nil)
		return
	}

	walker.secondaryTransaction = nil

	walker.mmu.vRR = (walker.mmu.vRR + 1) % uint64(len(lowModules))

	tracing.AddTaskStep(tracing.MsgIDAtReceiver(writeReq, walker.mmu),
		now, walker.mmu, "page_walk_store_l1")
	tracing.EndTask(walker.mmu.Name()+"stall", now, walker.mmu)
}

// NBWalkerMMU is the default mmu implementation. It is also an akita Component.
type NBWalkerMMU struct {
	akita.TickingComponent

	ToTop akita.Port
	L3TLB akita.Port

	ToPageWalkCache akita.Port
	PageWalkCache   akita.Port
	topSender       akitaext.BufferedSender

	TranslationPort   akita.Port
	translationSender akitaext.BufferedSender
	lowModuleFinder   cache.LowModuleFinder

	ToCache               akita.Port
	VCacheLowModuleFinder cache.LowModuleFinder
	VCacheControlFinder   cache.LowModuleFinder

	pageTable *device.PageTableImpl

	pageWalkers []*CaPWQPageWalker

	log2CacheLineSize uint64

	numInflightPTWRequests uint64

	pageWalkReqQueue []*device.TranslationReq
	pageWalkRspQueue []*transactionImpl

	walkReqQueueCapacity int
	walkRspQueueCapacity int

	monitorStats *monitor.CaPWQMonitorStats

	vRR uint64
}

func (impl *NBWalkerMMU) fillPageWalkCache(
	now akita.VTimeInSec,
	trans *transactionImpl,
) {
	level := uint64(trans.level)
	data := mmu.Uint64ToBytes(trans.PPN | level)

	writeReq := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(impl.ToPageWalkCache).
		WithDst(impl.PageWalkCache).
		WithPID(trans.pid).
		WithAddress(impl.pageTable.AlignToPage(trans.Address) | level).
		WithData(data).
		Build()

	// No need to check whether the send is successful.
	impl.ToPageWalkCache.Send(writeReq)
}

func (impl *NBWalkerMMU) SentCommand(info interface{}) {
	//TODO implement me
	panic("implement me")
}

func (impl *NBWalkerMMU) InitMonitorStats() {
	impl.monitorStats = &monitor.CaPWQMonitorStats{}
}

func (impl *NBWalkerMMU) ClearMonitorStats() {
	impl.monitorStats.Clear()
}

func (impl *NBWalkerMMU) GetMonitorStats() interface{} {
	return impl.monitorStats
}

// Tick defines how the MMU update state each cycle
func (impl *NBWalkerMMU) Tick(now akita.VTimeInSec) bool {
	impl.topSender.Tick(now)
	impl.translationSender.Tick(now)
	impl.processPageWalkReqQueue(now)
	impl.processPageWalkRspQueue(now)
	impl.parseFromMem(now)
	impl.parseFromL1(now)
	impl.parseFromPageWalkCache(now)
	impl.parseFromTop(now)

	if impl.isActive() {
		tracing.StartTask(
			"",
			"",
			now,
			impl,
			"num_active_walkers",
			strconv.Itoa(int(impl.numInflightPTWRequests)),
			nil,
		)
		tracing.StartTask(
			"",
			"",
			now,
			impl,
			"page_walk_queue_len",
			strconv.Itoa(len(impl.pageWalkReqQueue)),
			nil,
		)
		tracing.StartTask(
			"",
			"",
			now,
			impl,
			"page_walk_rsp_queue_len",
			strconv.Itoa(len(impl.pageWalkRspQueue)),
			nil,
		)
	}

	if impl.monitorStats != nil {
		impl.monitorStats.ReqLength = uint64(len(impl.pageWalkReqQueue))
		impl.monitorStats.RspLength = uint64(len(impl.pageWalkRspQueue))

		for i := range impl.pageWalkers {
			if impl.pageWalkers[i].transaction != nil {
				impl.monitorStats.ReqLength++
			}
		}

		impl.monitorStats.NumPTW = impl.numInflightPTWRequests
	}

	return true
}

func (impl *NBWalkerMMU) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: impl,
		Now:    now,
		Item:   what,
	}

	impl.InvokeHook(ctx)
}

func (impl *NBWalkerMMU) parseFromPageWalkCache(now akita.VTimeInSec) bool {
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
}

func (impl *NBWalkerMMU) parseFromMem(now akita.VTimeInSec) bool {
	item := impl.TranslationPort.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		impl.handleMemResponse(msg, now)
	default:
		panic("unknown message type")
	}

	return false
}

func (impl *NBWalkerMMU) handleMemResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) {
	for _, walker := range impl.pageWalkers {
		if walker.transaction == nil {
			continue
		}

		if walker.transaction.msgID == rsp.RespondTo {
			walker.AcceptMemRsp(now, rsp)

			impl.TranslationPort.Retrieve(now)

			return
		}
	}
	panic("response doesn't match any inflight transaction")
}

func (impl *NBWalkerMMU) parseFromL1(now akita.VTimeInSec) bool {
	item := impl.ToCache.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.DataReadyRsp:
		return impl.handleL1ReadResponse(msg, now)
	case *mem.WriteDoneRsp:
		impl.ToCache.Retrieve(now)

		return true
	default:
		panic("unknown message type")
	}
}

func (impl *NBWalkerMMU) handleL1ReadResponse(rsp *mem.DataReadyRsp, now akita.VTimeInSec) bool {
	if len(impl.pageWalkRspQueue) >= impl.walkRspQueueCapacity {
		return false
	}

	block := rsp.Info.(vm.NBWalkerBlock)

	newTrans := &transactionImpl{
		state:   l1Done,
		Address: block.Address,
		pid:     block.PID,
		msgID:   block.MsgID,
		PPN:     binary.LittleEndian.Uint64(rsp.Data),
		level:   block.Level,
	}

	if newTrans.level+1 == 4 {
		impl.finalizeTransaction(now, newTrans)
	} else {
		impl.fillPageWalkCache(now, newTrans)
	}
	newTrans.level++

	impl.pageWalkRspQueue = append(impl.pageWalkRspQueue, newTrans)

	impl.ToCache.Retrieve(now)

	return true
}

func (impl *NBWalkerMMU) finalizeTransaction(
	now akita.VTimeInSec,
	trans *transactionImpl,
) {
	if !impl.topSender.CanSend(1) {
		return
	}

	page, found := impl.pageTable.Find(trans.pid, trans.Address)
	if !found {
		panic("page not found")
	}

	pAddr := trans.PPN
	if pAddr != page.PAddr {
		panic("addresses don't match!")
	}

	newPage := device.Page{
		PID:   trans.pid,
		VAddr: trans.Address,
		PAddr: pAddr,
		Valid: true,
	}

	rsp := device.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(impl.ToTop).
		WithDst(impl.L3TLB).
		WithPage(newPage).
		Build()

	impl.topSender.Send(rsp)

	impl.numInflightPTWRequests--

	trans.state = transactionFinished

	tracing.EndTask(trans.msgID, now, impl)
}

func (impl *NBWalkerMMU) parseFromTop(now akita.VTimeInSec) bool {
	item := impl.ToTop.Peek()
	if item == nil {
		return false
	}

	if len(impl.pageWalkReqQueue) >= impl.walkReqQueueCapacity {
		return false
	}

	req, ok := item.(*device.TranslationReq)
	if !ok {
		panic(fmt.Sprintf("item isn't a translation request: %s", reflect.TypeOf(item)))
	}

	impl.pageWalkReqQueue = append(impl.pageWalkReqQueue, req)

	impl.ToTop.Retrieve(now)

	return false
}

func (impl *NBWalkerMMU) processPageWalkReqQueue(now akita.VTimeInSec) bool {
	if len(impl.pageWalkReqQueue) == 0 {
		return false
	}

	head := impl.pageWalkReqQueue[0]

	for _, walker := range impl.pageWalkers {
		if !walker.CanAcceptPrimary() {
			continue
		}

		walker.AcceptReqFromTop(
			now,
			head,
		)

		impl.numInflightPTWRequests++

		impl.pageWalkReqQueue = impl.pageWalkReqQueue[1:]

		return true
	}

	for _, walker := range impl.pageWalkers {
		if !walker.CanAccept() {
			continue
		}

		walker.AcceptReqFromTop(
			now,
			head,
		)

		impl.numInflightPTWRequests++

		impl.pageWalkReqQueue = impl.pageWalkReqQueue[1:]

		return true
	}

	return false
}

func (impl *NBWalkerMMU) processPageWalkRspQueue(now akita.VTimeInSec) bool {
	if len(impl.pageWalkRspQueue) == 0 {
		return false
	}

	head := impl.pageWalkRspQueue[0]

	for _, walker := range impl.pageWalkers {
		if !walker.CanAccept() {
			continue
		}

		walker.AcceptL1CacheRsp(
			now,
			head,
		)

		impl.pageWalkRspQueue = impl.pageWalkRspQueue[1:]

		return true
	}

	return false
}

// SetLowModuleFinder sets the table recording where to find an address.
func (impl *NBWalkerMMU) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	impl.lowModuleFinder = lmf
}

func (impl *NBWalkerMMU) GetNumActiveWalkers() int {
	num := 0
	for i := range impl.pageWalkers {
		if impl.pageWalkers[i].secondaryTransaction != nil {
			num++
		}
	}
	return num
}

func (impl *NBWalkerMMU) isActive() bool {
	for i := range impl.pageWalkers {
		if impl.pageWalkers[i].secondaryTransaction != nil {
			return true
		}
	}

	return false
}

func (impl *NBWalkerMMU) ToTopPort() akita.Port {
	return impl.ToTop
}

func (impl *NBWalkerMMU) ToTranslationPort() akita.Port {
	return impl.TranslationPort
}

func (impl *NBWalkerMMU) ToCachePort() akita.Port {
	return impl.ToCache
}

func (impl *NBWalkerMMU) CanAccept() bool {
	for i := range impl.pageWalkers {
		if impl.pageWalkers[i].CanAccept() {
			return true
		}
	}
	return false
}

func (impl *NBWalkerMMU) ToPageWalkCachePort() akita.Port {
	return impl.ToPageWalkCache
}

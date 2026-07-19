package cu

import (
	"log"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mgpusim/timing/wavefront"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

// A WalkerVectorMemoryUnit is the block in a compute unit that can performs vector
// memory operations.
type WalkerVectorMemoryUnit struct {
	cu *ComputeUnit

	scratchpadPreparer ScratchpadPreparer
	coalescer          coalescer

	numInstInFlight         uint64
	numTransactionInFlight  uint64
	maxInstructionsInFlight uint64

	instructionPipeline           pipelining.Pipeline
	postInstructionPipelineBuffer util.Buffer
	transactionsWaiting           []VectorMemAccessInfo
	transactionPipeline           pipelining.Pipeline
	postTransactionPipelineBuffer util.Buffer

	isIdle bool

	reachLimitation bool
}

// NewWalkerVectorMemoryUnit creates a new Vector Memory Unit.
func NewWalkerVectorMemoryUnit(
	cu *ComputeUnit,
	scratchpadPreparer ScratchpadPreparer,
	coalescer coalescer,
) *WalkerVectorMemoryUnit {
	u := new(WalkerVectorMemoryUnit)
	u.cu = cu

	u.scratchpadPreparer = scratchpadPreparer
	u.coalescer = coalescer

	return u
}

// CanAcceptWave checks if the buffer of the read stage is occupied or not
func (u *WalkerVectorMemoryUnit) CanAcceptWave() bool {
	return u.instructionPipeline.CanAccept()
}

// AcceptWave moves one wavefront into the read buffer of the Scalar unit
func (u *WalkerVectorMemoryUnit) AcceptWave(
	wave *wavefront.Wavefront,
	now akita.VTimeInSec,
) {
	u.instructionPipeline.Accept(now, vectorMemInst{wavefront: wave})
	u.numInstInFlight++
}

// IsIdle moves one wavefront into the read buffer of the Scalar unit
func (u *WalkerVectorMemoryUnit) IsIdle() bool {
	u.isIdle = (u.numInstInFlight == 0) && (u.numTransactionInFlight == 0)
	return u.isIdle
}

// Run executes three pipeline stages that are controlled by the
// WalkerVectorMemoryUnit
func (u *WalkerVectorMemoryUnit) Run(now akita.VTimeInSec) bool {
	madeProgress := false
	madeProgress = u.sendRequest(now) || madeProgress
	madeProgress = u.transactionPipeline.Tick(now) || madeProgress
	madeProgress = u.instToTransaction(now) || madeProgress
	madeProgress = u.instructionPipeline.Tick(now) || madeProgress
	return madeProgress
}

func (u *WalkerVectorMemoryUnit) instToTransaction(
	now akita.VTimeInSec,
) bool {
	if len(u.transactionsWaiting) > 0 {
		return u.insertTransactionToPipeline(now)
	}

	return u.execute(now)
}

func (u *WalkerVectorMemoryUnit) insertTransactionToPipeline(
	now akita.VTimeInSec,
) bool {
	if !u.transactionPipeline.CanAccept() {
		return false
	}

	u.transactionPipeline.Accept(now, u.transactionsWaiting[0])
	u.transactionsWaiting = u.transactionsWaiting[1:]

	return true
}

func (u *WalkerVectorMemoryUnit) execute(now akita.VTimeInSec) (madeProgress bool) {
	item := u.postInstructionPipelineBuffer.Pop()
	if item == nil {
		return false
	}

	wave := item.(vectorMemInst).wavefront

	if wave.Translation == nil {
		panic("Only translation wavefront can be executed in WalkerVectorMemoryUnit.")
	}

	return u.executeTranslation(now, wave)
}

func (u *WalkerVectorMemoryUnit) executeTranslation(
	now akita.VTimeInSec,
	wf *wavefront.Wavefront,
) bool {
	var ok bool

	switch wf.PC {
	case 0x90:
		ok = u.executePTELoad(wf)
	case 0xa0:
		ok = u.executePWCStore(wf)
	case 0xb8:
		ok = u.executePTERsp(wf)
	default:
		log.Panicf("PC %d in translation wavefront is not supported.", wf.PC)
	}

	if !ok {
		return false
	}

	wf.State = wavefront.WfReady
	wf.PC += 0x8

	u.numInstInFlight--

	return true
}

func (u *WalkerVectorMemoryUnit) executePTELoad(
	wave *wavefront.Wavefront,
) bool {
	transactions := u.coalescer.generateMemTransactions(wave)

	if len(transactions) == 0 {
		u.reachLimitation = false

		return true
	}

	if len(transactions)+len(u.cu.InFlightVectorPTEAccess) >
		u.cu.InFlightVectorMemAccessLimit {

		u.reachLimitation = true

		return false
	}

	wave.OutstandingVectorMemAccess += len(transactions)

	for i, t := range transactions {
		u.cu.InFlightVectorPTEAccess = append(u.cu.InFlightVectorPTEAccess, t)
		if i != len(transactions)-1 {
			t.Read.CanWaitForCoalesce = true
		}
		lowModule := u.cu.L2CacheModules.Find(t.Read.Address)
		t.Read.Dst = lowModule
		t.Read.Src = u.cu.ToL2
		t.Read.PTW = true
		u.transactionsWaiting = append(u.transactionsWaiting, t)
	}

	u.reachLimitation = false

	return true
}

func (u *WalkerVectorMemoryUnit) executePWCStore(
	wave *wavefront.Wavefront,
) bool {
	transactions := u.coalescer.generateMemTransactions(wave)

	if len(transactions) == 0 {
		u.reachLimitation = false

		return true
	}

	for _, t := range transactions {
		t.Write.Dst = u.cu.PWC
		t.Write.Src = u.cu.ToL3TLB
		t.Write.PTW = true
		u.transactionsWaiting = append(u.transactionsWaiting, t)
	}

	u.reachLimitation = false

	return true
}

func (u *WalkerVectorMemoryUnit) executePTERsp(
	wave *wavefront.Wavefront,
) bool {
	transactions := u.coalescer.generateMemTransactions(wave)

	if len(transactions) == 0 {
		u.reachLimitation = false

		return true
	}

	for _, t := range transactions {
		t.Translation.Dst = u.cu.L3TLB
		t.Translation.Src = u.cu.ToL3TLB
		t.Translation.PTW = true
		u.transactionsWaiting = append(u.transactionsWaiting, t)
	}

	u.reachLimitation = false

	return true
}

func (u *WalkerVectorMemoryUnit) sendRequest(now akita.VTimeInSec) bool {
	item := u.postTransactionPipelineBuffer.Peek()
	if item == nil {
		return false
	}

	info := item.(VectorMemAccessInfo)
	if info.Wavefront.Translation == nil {
		panic("Only translation wavefront can be executed in WalkerVectorMemoryUnit.")
	}

	switch info.PC {
	case 0x90:
		return u.sendPTEReadRequest(info, now)
	case 0xa0:
		return u.sendPWCStoreRequest(info, now)
	case 0xb8:
		return u.sendPTERsp(info, now)
	default:
		log.Panicf("PC %#x in translation wavefront is not supported.", info.PC)
	}
	panic("unreachable")
}

func (u *WalkerVectorMemoryUnit) sendPTEReadRequest(
	info VectorMemAccessInfo,
	now akita.VTimeInSec,
) bool {
	req := info.Read

	req.Meta().SendTime = now
	err := u.cu.ToL2.Send(req)
	if err == nil {
		u.postTransactionPipelineBuffer.Pop()
		u.numTransactionInFlight--

		tracing.TraceReqInitiate(req, now, u.cu, info.Wavefront.Translation.ID)

		return true
	}

	return false
}

func (u *WalkerVectorMemoryUnit) sendPWCStoreRequest(
	info VectorMemAccessInfo,
	now akita.VTimeInSec,
) bool {
	req := info.Write

	req.Meta().SendTime = now
	err := u.cu.ToL3TLB.Send(req)
	if err == nil {
		u.postTransactionPipelineBuffer.Pop()
		u.numTransactionInFlight--

		return true
	}

	return false
}

func (u *WalkerVectorMemoryUnit) sendPTERsp(
	info VectorMemAccessInfo,
	now akita.VTimeInSec,
) bool {
	rsp := info.Translation

	rsp.Meta().SendTime = now
	err := u.cu.ToL3TLB.Send(rsp)
	if err == nil {
		u.postTransactionPipelineBuffer.Pop()
		u.numTransactionInFlight--

		return true
	}

	return false
}

// Flush flushes
func (u *WalkerVectorMemoryUnit) Flush() {
	u.instructionPipeline.Clear()
	u.transactionPipeline.Clear()
	u.postInstructionPipelineBuffer.Clear()
	u.postTransactionPipelineBuffer.Clear()
	u.transactionsWaiting = nil
	u.numInstInFlight = 0
	u.numTransactionInFlight = 0
}

func (u *WalkerVectorMemoryUnit) GetName() string {
	return u.cu.Name() + ".WalkerVectorMemoryUnit"
}

func (u *WalkerVectorMemoryUnit) CheckTopPort(port akita.Port) bool {
	panic("vector memory unit has no top port")
}

func (u *WalkerVectorMemoryUnit) CheckBottomPort(port akita.Port) bool {
	return port == u.cu.ToVectorMem
}

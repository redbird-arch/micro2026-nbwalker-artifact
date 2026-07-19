package lds

import (
	"strconv"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/util"
	"gitlab.com/akita/util/ca"
	"gitlab.com/akita/util/pipelining"
	"gitlab.com/akita/util/tracing"
)

type pipelineItem struct {
	taskID string
	msg    akita.Msg
}

func (item *pipelineItem) TaskID() string {
	return item.taskID
}

type IdealLDSEntry struct {
	PID     ca.PID
	Address uint64
	PPN     uint64
}

type IdealCaPWQLDS struct {
	*akita.TickingComponent

	numReqPerCycle int

	mmuSidePort     akita.Port
	pipeline        pipelining.Pipeline
	postPipelineBuf util.Buffer

	storage []IdealLDSEntry
}

func NewIdealCaPWQLDS(
	name string,
	engine akita.Engine,
	freq akita.Freq,
	numReqPerCycle int,
	latency int,
) *IdealCaPWQLDS {
	c := &IdealCaPWQLDS{
		numReqPerCycle: numReqPerCycle,
	}

	c.TickingComponent = akita.NewTickingComponent(
		name, engine, freq, c)

	c.mmuSidePort = akita.NewLimitNumMsgPort(
		c,
		numReqPerCycle,
		name+".MMUSidePort",
	)

	c.postPipelineBuf = util.NewBuffer(numReqPerCycle)
	c.pipeline = pipelining.MakeBuilder().
		WithPipelineWidth(numReqPerCycle).
		WithNumStage(latency).
		WithCyclePerStage(1).
		WithPostPipelineBuffer(c.postPipelineBuf).
		Build(name + ".Pipeline")

	c.storage = make([]IdealLDSEntry, 0)

	return c
}

func (c *IdealCaPWQLDS) trace(now akita.VTimeInSec, what string) {
	ctx := akita.HookCtx{
		Domain: c,
		Now:    now,
		Item:   what,
	}

	c.InvokeHook(ctx)
}

func (c *IdealCaPWQLDS) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = madeProgress || c.processingPipeline(now)
		madeProgress = madeProgress || c.parseFromMMU(now)
	}
	madeProgress = madeProgress || c.pipeline.Tick(now)

	return madeProgress
}

func (c *IdealCaPWQLDS) parseFromMMU(now akita.VTimeInSec) bool {
	item := c.mmuSidePort.Peek()
	if item == nil {
		return false
	}

	switch msg := item.(type) {
	case *mem.ReadReq, *mem.WriteReq:
		return c.processingRequests(now, msg)
	default:
		panic("Unsupported message type.")
	}

	return false
}

func (c *IdealCaPWQLDS) processingRequests(
	now akita.VTimeInSec,
	req akita.Msg,
) bool {
	if !c.pipeline.CanAccept() {
		return false
	}

	item := &pipelineItem{
		taskID: akita.GetIDGenerator().Generate(),
		msg:    req,
	}

	c.pipeline.Accept(now, item)
	c.mmuSidePort.Retrieve(now)

	return true
}

func (c *IdealCaPWQLDS) processingPipeline(now akita.VTimeInSec) bool {
	item := c.postPipelineBuf.Peek()
	if item == nil {
		return false
	}

	pItem := item.(*pipelineItem)
	switch msg := pItem.msg.(type) {
	case *mem.ReadReq:
		return c.handleReadReq(now, msg)
	case *mem.WriteReq:
		return c.handleWriteReq(now, msg)
	default:
		panic("Unsupported message type.")
	}

	return false
}

func (c *IdealCaPWQLDS) handleReadReq(
	now akita.VTimeInSec,
	req *mem.ReadReq,
) bool {
	entries := make([]IdealLDSEntry, 0)

	if len(c.storage) != 0 {
		entries = append(entries, c.storage[0])
	}

	rsp := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.mmuSidePort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithInfo(entries).
		Build()

	rsp.TrafficBytes += 12 * len(entries)

	err := c.mmuSidePort.Send(rsp)
	if err != nil {
		return false
	}

	if len(c.storage) != 0 {
		c.storage = c.storage[1:]
	}

	c.postPipelineBuf.Pop()

	return true
}

func (c *IdealCaPWQLDS) handleWriteReq(
	now akita.VTimeInSec,
	req *mem.WriteReq,
) bool {
	entry := req.Info.(IdealLDSEntry)

	c.storage = append(c.storage, entry)

	done := mem.WriteDoneRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.mmuSidePort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		Build()

	err := c.mmuSidePort.Send(done)
	if err != nil {
		return false
	}

	c.postPipelineBuf.Pop()

	tracing.StartTask(
		"",
		"",
		now,
		c,
		"capwq_lds_len",
		strconv.Itoa(int(len(c.storage))),
		nil,
	)

	return true
}

func (c *IdealCaPWQLDS) GetTopPort() akita.Port {
	panic("not implemented")
}

func (c *IdealCaPWQLDS) GetBottomPort() akita.Port {
	panic("not implemented")
}

func (c *IdealCaPWQLDS) GetControlPort() akita.Port {
	panic("not implemented")
}

func (c *IdealCaPWQLDS) GetMMUSidePort() akita.Port {
	return c.mmuSidePort
}

func (c *IdealCaPWQLDS) GetName() string {
	return c.Name()
}

func (c *IdealCaPWQLDS) SetLowModuleFinder(lmf cache.LowModuleFinder) {
	panic("not implemented")
}

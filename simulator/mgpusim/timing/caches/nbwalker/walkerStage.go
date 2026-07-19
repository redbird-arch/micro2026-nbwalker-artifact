package NBWalker

import (
	"log"
	"reflect"

	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/cache"
	"gitlab.com/akita/util/tracing"
)

type walkerStage struct {
	cache *Cache
}

func (c *walkerStage) Reset() {}

func (c *walkerStage) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = c.processTransFromBuf(now) || madeProgress
	madeProgress = c.processReqFromWalker(now) || madeProgress

	return madeProgress
}

func (c *walkerStage) processTransFromBuf(now akita.VTimeInSec) bool {
	item := c.cache.walkerDirBuf.Peek()
	if item == nil {
		return false
	}

	trans := item.(*transaction)

	if trans.fromWalker {
		return c.processWalkerReq(now, trans)
	}
	panic("transaction in walkerDirBuf is not from walker")
}

func (c *walkerStage) processReqFromWalker(
	now akita.VTimeInSec,
) bool {
	item := c.cache.WalkerPort.Peek()
	if item == nil {
		return false
	}

	if msg, ok := item.(*mem.ControlMsg); ok {
		c.cache.numReservedPTWEntry = msg.Info.(int)

		c.cache.WalkerPort.Retrieve(now)
		return true
	}

	if !c.cache.walkerDirBuf.CanPush() {
		return false
	}

	req := item.(mem.AccessReq)

	trans := c.createTransaction(req)
	c.cache.transactions = append(c.cache.transactions, trans)
	c.cache.postCoalesceTransactions = append(c.cache.postCoalesceTransactions,
		trans)

	c.cache.walkerDirBuf.Push(trans)

	c.cache.WalkerPort.Retrieve(now)

	tracing.TraceReqReceive(req, now, c.cache)

	return true
}

func (c *walkerStage) createTransaction(req mem.AccessReq) *transaction {
	switch req := req.(type) {
	case *mem.ReadReq:
		t := &transaction{
			read:       req,
			fromWalker: true,
		}
		return t
	case *mem.WriteReq:
		t := &transaction{
			write:      req,
			fromWalker: true,
		}
		return t
	default:
		log.Panicf("cannot process request of type %s\n", reflect.TypeOf(req))
		return nil
	}
}

func (c *walkerStage) processWalkerReq(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	if trans.write == nil {
		panic("WalkerReq called with nil transaction")
	}

	return c.processWalkerWrite(now, trans)
}

func (c *walkerStage) processWalkerWrite(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write
	addr := write.Address
	pid := write.PID
	PTEBlockSize := uint64(1 << (c.cache.log2BlockSize + c.cache.extendBits))
	PTEBlockID := addr / PTEBlockSize * PTEBlockSize

	mshrEntry := c.cache.mshr.QueryForWalker(
		pid,
		PTEBlockID,
	)
	if mshrEntry != nil {
		offset := (addr >> c.cache.log2BlockSize) & c.cache.offsetMask

		if mshrEntry.OffsetBits[int(offset)] {
			return c.processWalkerWriteMSHRHit(
				now,
				trans,
				mshrEntry,
			)
		}
		return c.processWalkerWritePartialMSHRHit(
			now,
			trans,
			mshrEntry,
		)
	}

	if c.cache.mshr.IsFull() {
		return false
	}

	if !c.fetchPTEsFromBottom(now, trans) {
		return false
	}

	c.cache.walkerDirBuf.Pop()

	return true
}

func (c *walkerStage) fetchPTEsFromBottom(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << c.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	bottomModule := c.cache.lowModuleFinder.Find(cacheLineID)
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	readToBottom := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.cache.BottomPort).
		WithDst(bottomModule).
		WithAddress(cacheLineID).
		WithPID(pid).
		WithByteSize(blockSize).
		WithInfo(readReqInfo).
		Build()

	readToBottom.PTW = true

	err := c.cache.BottomPort.Send(readToBottom)
	if err != nil {
		return false
	}

	tracing.AddTaskStep(
		trans.id,
		now,
		c.cache,
		"ptw-read-miss",
	)

	tracing.TraceReqInitiate(readToBottom, now, c.cache, trans.id)
	trans.readToBottom = readToBottom

	PTEBlockSize := uint64(1 << (c.cache.log2BlockSize + c.cache.extendBits))
	PTEBlockID := addr / PTEBlockSize * PTEBlockSize
	PTEOffset := (addr >> c.cache.log2BlockSize) & c.cache.offsetMask

	mshrEntry := c.cache.mshr.AddForWalker(pid, PTEBlockID, PTEOffset, 1<<int(c.cache.extendBits))
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.ReadReq = readToBottom

	return true
}

func (c *walkerStage) processWalkerWriteMSHRHit(
	now akita.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	if len(mshrEntry.Requests) >= 8 {
		return false
	}

	mshrEntry.Requests = append(mshrEntry.Requests, trans)

	c.cache.walkerDirBuf.Pop()

	tracing.AddTaskStep(
		trans.id,
		now,
		c.cache,
		"ptw-read-mshr-hit",
	)

	return true
}

func (c *walkerStage) processWalkerWritePartialMSHRHit(
	now akita.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	if len(mshrEntry.Requests) >= 8 {
		return false
	}

	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << c.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	bottomModule := c.cache.lowModuleFinder.Find(cacheLineID)
	readReqInfo := &mem.ReadReqInfo{ReturnAccessInfo: true}
	readToBottom := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.cache.BottomPort).
		WithDst(bottomModule).
		WithAddress(cacheLineID).
		WithPID(pid).
		WithByteSize(blockSize).
		WithInfo(readReqInfo).
		Build()

	readToBottom.PTW = true

	err := c.cache.BottomPort.Send(readToBottom)
	if err != nil {
		return false
	}

	tracing.AddTaskStep(
		trans.id,
		now,
		c.cache,
		"ptw-read-mshr-partial-hit",
	)

	tracing.TraceReqInitiate(readToBottom, now, c.cache, trans.id)
	trans.readToBottom = readToBottom

	PTEOffset := (addr >> c.cache.log2BlockSize) & c.cache.offsetMask

	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.OffsetBits[int(PTEOffset)] = true

	c.cache.walkerDirBuf.Pop()

	return true
}

package NBWalker

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/util/tracing"
)

type respondStage struct {
	cache *Cache
}

func (s *respondStage) Tick(now akita.VTimeInSec) bool {
	if len(s.cache.transactions) == 0 {
		return false
	}

	for _, trans := range s.cache.transactions {
		if !trans.done {
			continue
		}

		if trans.fromWalker {
			if trans.write == nil {
				panic("walker transaction should have write")
			}
			return s.respondWalkerWriteTrans(now, trans)
		}

		if trans.read != nil {
			return s.respondReadTrans(now, trans)
		}
		return s.respondWriteTrans(now, trans)
	}

	return false
}

func (s *respondStage) respondWalkerWriteTrans(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	if !trans.done {
		return false
	}

	write := trans.write
	dr := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(s.cache.WalkerPort).
		WithDst(write.Src).
		WithRspTo(write.ID).
		WithData(trans.data).
		WithInfo(trans.write.Info).
		Build()
	err := s.cache.WalkerPort.Send(dr)
	if err != nil {
		return false
	}

	s.removeTransaction(trans)

	tracing.TraceReqComplete(write, now, s.cache)

	return true
}

func (s *respondStage) respondWalkerReadTrans(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	if !trans.done {
		return false
	}

	read := trans.read

	s.removeTransaction(trans)

	tracing.TraceReqComplete(read, now, s.cache)

	return true
}

func (s *respondStage) respondReadTrans(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	if !trans.done {
		return false
	}

	read := trans.read
	dr := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(s.cache.TopPort).
		WithDst(read.Src).
		WithRspTo(read.ID).
		WithData(trans.data).
		Build()
	err := s.cache.TopPort.Send(dr)
	if err != nil {
		return false
	}

	s.removeTransaction(trans)

	tracing.TraceReqComplete(read, now, s.cache)

	return true
}

func (s *respondStage) respondWriteTrans(
	now akita.VTimeInSec,
	trans *transaction,
) bool {
	if !trans.done {
		return false
	}

	write := trans.write
	done := mem.WriteDoneRspBuilder{}.
		WithSendTime(now).
		WithSrc(s.cache.TopPort).
		WithDst(write.Src).
		WithRspTo(write.ID).
		Build()
	err := s.cache.TopPort.Send(done)
	if err != nil {
		return false
	}

	s.removeTransaction(trans)

	tracing.TraceReqComplete(write, now, s.cache)

	return true
}

func (s *respondStage) removeTransaction(trans *transaction) {
	for i, t := range s.cache.transactions {
		if t == trans {
			s.cache.transactions = append(s.cache.transactions[:i],
				s.cache.transactions[i+1:]...)
			return
		}
	}

	panic("not found")
}

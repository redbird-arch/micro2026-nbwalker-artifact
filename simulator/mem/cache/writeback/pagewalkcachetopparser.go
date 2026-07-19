package writeback

import (
	"gitlab.com/akita/akita"
	"gitlab.com/akita/mem"
	"gitlab.com/akita/util/tracing"
)

type pageWalkCacheTopParser struct {
	cache *PageWalkCache
}

func (p *pageWalkCacheTopParser) Tick(now akita.VTimeInSec) bool {
	madeProgress := false

	madeProgress = p.parseFromTop(now) || madeProgress
	madeProgress = p.parseFromMMUs(now) || madeProgress

	return madeProgress
}

func (p *pageWalkCacheTopParser) parseFromTop(now akita.VTimeInSec) bool {
	if p.cache.state != cacheStateRunning {
		return false
	}
	req := p.cache.TopPort.Peek()
	if req == nil {
		return false
	}

	// instead wrap in pipeline object and push into pipeline
	if !p.cache.pipeline.CanAccept() {
		return false
	}
	// if !p.cache.dirStageBuffer.CanPush() {
	// return false
	// }

	trans := &transaction{}
	switch req := req.(type) {
	case *mem.ReadReq:
		trans.read = req
	default:
		panic("unexpected req type")
	}
	// pipeline
	pipelineItem := pwcPipelineItem{
		taskID: akita.GetIDGenerator().Generate(),
		trans:  trans,
	}
	p.cache.pipeline.Accept(now, pipelineItem)
	// p.cache.dirStageBuffer.Push(trans)

	p.cache.inFlightTransactions = append(p.cache.inFlightTransactions, trans)
	tracing.TraceReqReceive(req, now, p.cache)

	p.cache.TopPort.Retrieve(now)

	return true
}

func (p *pageWalkCacheTopParser) parseFromMMUs(now akita.VTimeInSec) bool {
	if p.cache.state != cacheStateRunning {
		return false
	}
	req := p.cache.ToMMUs.Peek()
	if req == nil {
		return false
	}

	// instead wrap in pipeline object and push into pipeline
	if !p.cache.pipeline.CanAccept() {
		return false
	}
	// if !p.cache.dirStageBuffer.CanPush() {
	// return false
	// }

	trans := &transaction{}
	switch req := req.(type) {
	case *mem.WriteReq:
		trans.write = req
	default:
		panic("unexpected req type")
	}
	// pipeline
	pipelineItem := pwcPipelineItem{
		taskID: akita.GetIDGenerator().Generate(),
		trans:  trans,
	}
	p.cache.pipeline.Accept(now, pipelineItem)
	// p.cache.dirStageBuffer.Push(trans)

	p.cache.inFlightTransactions = append(p.cache.inFlightTransactions, trans)
	tracing.TraceReqReceive(req, now, p.cache)

	p.cache.ToMMUs.Retrieve(now)

	return true
}

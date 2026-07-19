package cu

import (
	"log"

	"gitlab.com/akita/mem"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/mem/vm/mmu"
	"gitlab.com/akita/mgpusim/timing/wavefront"
)

type walkerCoalescer struct {
	log2CacheLineSize uint64
}

func (c walkerCoalescer) generateMemTransactions(
	wf *wavefront.Wavefront,
) []VectorMemAccessInfo {
	var transactions []VectorMemAccessInfo
	if wf.Translation == nil {
		panic("translation is nil, should not be in the walker coalescer")
	}

	switch wf.PC {
	case 0x90:
		reqs := c.generatePTEReadReqs(wf)
		transactions = c.generatePTEReadTransactions(wf, reqs)
	case 0xa0:
		reqs := c.generatePWCWriteReqs(wf)
		transactions = c.generatePWCWriteTransactions(wf, reqs)
	case 0xb8:
		rsps := c.generatePTETransRsps(wf)
		transactions = c.generatePTWTransTransactions(wf, rsps)
	default:
		log.Panicf("PC %d in translation wavefront is not supported.", wf.PC)
	}

	return transactions
}

func (c walkerCoalescer) generatePTEReadReqs(
	wf *wavefront.Wavefront,
) []*mem.ReadReq {
	reqs := []*mem.ReadReq{}

	for i := uint(0); i < 64; i++ {
		if !laneMasked(wf.EXEC, i) {
			continue
		}

		addr := wf.Translation.GetPTEPhyAddr(int(i))
		c.findOrCreateReadReq(&reqs, addr)
	}

	return reqs
}

func (c walkerCoalescer) generatePTETransRsps(
	wf *wavefront.Wavefront,
) []*device.TranslationRsp {
	rsps := []*device.TranslationRsp{}

	for i := uint(0); i < 64; i++ {
		t := wf.Translation.Threads[i]
		if t.Status != wavefront.Finished {
			continue
		}

		rsp := device.TranslationRspBuilder{}.
			WithRspTo(t.OriginalRequest.ID).
			WithPage(t.Page).
			Build()

		rsps = append(rsps, rsp)
	}

	return rsps
}

func (c walkerCoalescer) generatePWCWriteReqs(
	wf *wavefront.Wavefront,
) []*mem.WriteReq {
	exec := wf.EXEC
	reqs := []*mem.WriteReq{}

	for i := uint(0); i < 64; i++ {
		if !laneMasked(exec, i) {
			continue
		}

		t := wf.Translation.Threads[i]

		level := uint64(t.Level) - 1
		data := mmu.Uint64ToBytes(t.PPN | level)

		writeReq := mem.WriteReqBuilder{}.
			WithAddress(wf.Translation.AlignToPage(t.OriginalRequest.VAddr) | level).
			WithData(data).
			WithPID(t.OriginalRequest.PID).
			Build()

		reqs = append(reqs, writeReq)
	}

	return reqs
}

func (c walkerCoalescer) generatePTEReadTransactions(
	wf *wavefront.Wavefront,
	reqs []*mem.ReadReq,
) []VectorMemAccessInfo {
	transactions := []VectorMemAccessInfo{}
	for _, req := range reqs {
		transaction := VectorMemAccessInfo{
			Read:      req,
			Wavefront: wf,
			PC:        wf.PC,
		}

		c.addPTELaneInfo(&transaction, wf)

		transactions = append(transactions, transaction)
	}
	return transactions
}

func (c walkerCoalescer) generatePTWTransTransactions(
	wf *wavefront.Wavefront,
	rsps []*device.TranslationRsp,
) []VectorMemAccessInfo {
	transactions := []VectorMemAccessInfo{}
	for _, rsp := range rsps {
		transaction := VectorMemAccessInfo{
			Translation: rsp,
			Wavefront:   wf,
			PC:          wf.PC,
		}

		transactions = append(transactions, transaction)
	}
	return transactions
}

func (c walkerCoalescer) generatePWCWriteTransactions(
	wf *wavefront.Wavefront,
	reqs []*mem.WriteReq,
) []VectorMemAccessInfo {
	transactions := []VectorMemAccessInfo{}
	for _, req := range reqs {
		transaction := VectorMemAccessInfo{
			Write:     req,
			Wavefront: wf,
			PC:        wf.PC,
		}

		transactions = append(transactions, transaction)
	}
	return transactions
}

func (c walkerCoalescer) findOrCreateReadReq(
	reqs *[]*mem.ReadReq,
	addr uint64,
) *mem.ReadReq {
	for _, req := range *reqs {
		if c.isInSameCacheLine(addr, req.Address) {
			return req
		}
	}

	req := mem.ReadReqBuilder{}.
		WithAddress(c.cacheLineID(addr)).
		WithByteSize(1 << c.log2CacheLineSize).
		Build()
	*reqs = append(*reqs, req)
	return req
}

func (c walkerCoalescer) addPTELaneInfo(
	transaction *VectorMemAccessInfo,
	wf *wavefront.Wavefront,
) {
	exec := wf.EXEC
	req := transaction.Read

	for i := uint(0); i < 64; i++ {
		if !laneMasked(exec, i) {
			continue
		}

		addr := wf.Translation.GetPTEPhyAddr(int(i))
		if c.isInSameCacheLine(addr, req.Address) {
			laneInfo := vectorMemAccessLaneInfo{
				laneID:                int(i),
				addrOffsetInCacheLine: c.addrOffsetInCacheLine(addr),
			}
			transaction.laneInfo = append(transaction.laneInfo, laneInfo)
		}
	}
}

func (c walkerCoalescer) isInSameCacheLine(addr1, addr2 uint64) bool {
	return c.cacheLineID(addr1) == c.cacheLineID(addr2)
}

func (c walkerCoalescer) cacheLineID(addr uint64) uint64 {
	return addr >> c.log2CacheLineSize << c.log2CacheLineSize
}

func (c walkerCoalescer) addrOffsetInCacheLine(addr uint64) uint64 {
	return addr & ((1 << c.log2CacheLineSize) - 1)
}

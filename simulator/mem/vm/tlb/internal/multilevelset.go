package internal

import (
	"fmt"

	"github.com/google/btree"
	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/ca"
)

func NewMultiLevelSet(numWays int, log2PageSize uint64) Set {
	s := &multiLevelSetImpl{}
	s.blocks = make([]*block, numWays)
	s.visitTree = btree.New(2)
	s.vAddrWayIDMap = make([]map[string]int, 8)
	s.log2PageSize = log2PageSize
	for level := 0; level < 8; level++ {
		s.vAddrWayIDMap[level] = make(map[string]int)
	}
	for i := range s.blocks {
		b := &block{}
		s.blocks[i] = b
		b.wayID = i
		s.Visit(i)
	}
	return s
}

type multiLevelSetImpl struct {
	blocks        []*block
	vAddrWayIDMap []map[string]int
	visitTree     *btree.BTree
	visitCount    uint64
	log2PageSize  uint64
}

func (s *multiLevelSetImpl) keyString(
	pid ca.PID,
	vpn uint64,
) string {
	return fmt.Sprintf("%d%016x", pid, vpn)
}

func (s *multiLevelSetImpl) Lookup(pid ca.PID, vAddr uint64) (
	wayID int,
	page device.Page,
	found bool,
) {
	vpn := vAddr >> s.log2PageSize

	for level := 7; level >= 0; level-- {
		if level > 0 {
			groupStride := uint64(1) << (3 * (level - 1))
			mask := ^(uint64(8)*groupStride - 1)

			vpn = (vAddr >> s.log2PageSize) & mask
		}

		key := s.keyString(pid, vpn)
		wayID, ok := s.vAddrWayIDMap[level][key]

		if !ok {
			continue
		}

		block := s.blocks[wayID]

		if block.page.SizeBits != uint8(level) {
			panic("page size mismatch")
		}

		return block.wayID, block.page, true
	}

	return 0, device.Page{}, false
}

func (s *multiLevelSetImpl) Update(wayID int, page device.Page) {
	block := s.blocks[wayID]

	for level := int(block.page.SizeBits); level >= 0; level-- {
		for key, id := range s.vAddrWayIDMap[level] {
			if id == wayID {
				delete(s.vAddrWayIDMap[level], key)
			}
		}
	}

	vpn := page.VAddr >> s.log2PageSize

	groupStride := uint64(1) << (3 * page.SizeBits)
	mask := ^(uint64(8)*groupStride - 1)

	baseVPN := vpn & mask

	for idx := uint8(0); idx < 8; idx++ {
		if (page.ValidBits & (1 << idx)) != 0 {
			key := s.keyString(page.PID, baseVPN+uint64(idx)*groupStride)

			s.vAddrWayIDMap[page.SizeBits][key] = wayID
		}
	}

	block.page = page
}

func (s *multiLevelSetImpl) Evict() (wayID int, ok bool) {
	if s.hasNothingToEvict() {
		return 0, false
	}

	wayID = s.visitTree.DeleteMin().(*block).wayID
	return wayID, true
}

func (s *multiLevelSetImpl) Visit(wayID int) int {
	visitedBlock := s.blocks[wayID]

	rank := 0
	findRank := func(i btree.Item) bool {
		if i.(*block).lastVisit > visitedBlock.lastVisit {
			rank++
		}
		return true
	}
	s.visitTree.AscendGreaterOrEqual(visitedBlock, findRank)

	s.visitTree.Delete(visitedBlock)

	s.visitCount++
	visitedBlock.lastVisit = s.visitCount
	s.visitTree.ReplaceOrInsert(visitedBlock)

	// fmt.Println(rank)

	return rank
}

func (s *multiLevelSetImpl) hasNothingToEvict() bool {
	return s.visitTree.Len() == 0
}

package device

import (
	"gitlab.com/akita/mem"
)

// A deviceDemandPagingMemoryState implements DeviceMemoryState as a interleaved allocator
type deviceDemandPagingMemoryState struct {
	bankSize                       uint64
	numChiplets                    uint64
	numBankPerChiplet              uint64
	log2PageSize                   uint64
	initialAddress                 uint64
	storageSize                    uint64
	availablePAddrs                []uint64
	pAddrsForPageTable             [][]uint64
	log2MemoryBankInterleavingSize uint64
}

func (dims *deviceDemandPagingMemoryState) setInitialAddress(addr uint64) {
	dims.assertAddrIsPageAligned(addr)
	dims.initialAddress = addr
	dims.availablePAddrs = make([]uint64, 0)
	dims.pAddrsForPageTable = make([][]uint64, dims.numChiplets)
	pageSize := uint64(1 << dims.log2PageSize)
	endAddr := dims.initialAddress + dims.storageSize - 1*mem.GB
	for addr := dims.initialAddress; addr < endAddr; addr += pageSize {
		dims.availablePAddrs = append(dims.availablePAddrs, addr)
	}
	for addr := endAddr; addr < endAddr+1*mem.GB; {
		for i := 0; i < int(dims.numChiplets); i++ {
			for j := 0; j < int(dims.numBankPerChiplet); j++ {
				dims.pAddrsForPageTable[i] = append(dims.pAddrsForPageTable[i], addr)
				addr += uint64(1 << dims.log2MemoryBankInterleavingSize)
			}
		}
	}
}

func (dims *deviceDemandPagingMemoryState) assertAddrIsPageAligned(addr uint64) {
	if ((addr >> dims.log2PageSize) << dims.log2PageSize) != addr {
		panic("oh no!")
	}
}

func newdeviceDemandPagingMemoryState(log2pagesize uint64) DeviceMemoryState {
	return &deviceDemandPagingMemoryState{
		log2PageSize:                   log2pagesize,
		numChiplets:                    4,
		numBankPerChiplet:              16,
		bankSize:                       256 * mem.MB,
		log2MemoryBankInterleavingSize: 12,
	}
}

func (dims *deviceDemandPagingMemoryState) getInitialAddress() uint64 {
	return dims.initialAddress
}

func (dims *deviceDemandPagingMemoryState) setStorageSize(size uint64) {
	dims.storageSize = size
}

func (dims *deviceDemandPagingMemoryState) getStorageSize() uint64 {
	return dims.storageSize
}

func (dims *deviceDemandPagingMemoryState) addSinglePAddr(addr uint64) {
	dims.availablePAddrs = append(dims.availablePAddrs, addr)
}

func (dims *deviceDemandPagingMemoryState) popNextAvailablePAddrs() uint64 {
	nextPAddr := dims.availablePAddrs[0]
	dims.availablePAddrs = dims.availablePAddrs[1:]
	return nextPAddr
}

func (dims *deviceDemandPagingMemoryState) noAvailablePAddrs() bool {
	return len(dims.availablePAddrs) == 0
}

func (dims *deviceDemandPagingMemoryState) allocateMultiplePages(
	numPages int,
) (pAddrs []uint64) {
	for i := 0; i < numPages; i++ {
		pAddr := dims.popNextAvailablePAddrs()
		pAddrs = append(pAddrs, pAddr)
	}
	return pAddrs
}

func (dims *deviceDemandPagingMemoryState) allocatePageTablePage(vAddr, pAddr uint64) uint64 {
	chipletNum := vAddr & 3
	return dims.allocatePageTableOnChiplet(chipletNum)
}

func (dims *deviceDemandPagingMemoryState) allocatePageTableOnChiplet(
	chiplet uint64,
) (pAddr uint64) {
	pAddr = dims.pAddrsForPageTable[chiplet][0]
	dims.pAddrsForPageTable[chiplet] = dims.pAddrsForPageTable[chiplet][1:]
	return pAddr
}

func (dims *deviceDemandPagingMemoryState) allocatePageOnChiplet(chiplet int) uint64 {
	panic("not implemented")
	return 0
}

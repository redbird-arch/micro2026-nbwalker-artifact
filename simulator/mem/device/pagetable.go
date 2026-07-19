package device

import (
	"container/list"
	"fmt"
	"log"
	"sync"

	"gitlab.com/akita/util/ca"
)

// A Page is an entry in the page table, maintaining the information about how
// to translate a virtual address to a physical address
type Page struct {
	PID         ca.PID
	PAddr       uint64
	VAddr       uint64
	PageSize    uint64
	Valid       bool
	DeviceID    uint64
	Unified     bool
	IsMigrating bool
	IsPinned    bool
	SizeBits    uint8
	ValidBits   uint8
}

// A PageTable holds the a list of pages.
type PageTable interface {
	Insert(page Page)
	Remove(pid ca.PID, vAddr uint64)
	Find(pid ca.PID, Addr uint64) (Page, bool)
	Update(page Page)
	FindAddr(pid ca.PID, vAddr uint64, level uint64) uint64
	PageTablePagesAsBytes(pid ca.PID) []uint64
	PageSize() uint64
	AllocateMultiplePages(
		pid ca.PID,
		gpuID uint64,
		vAddr uint64,
		numPages int,
		isUnified bool,
	) []Page
	// GetPageTableAsBuffer(pid ca.PID)
}

// NewPageTable creates a new page table
func NewPageTable(log2PageSize uint64) *PageTableImpl {
	return &PageTableImpl{
		Log2PageSize: log2PageSize,
		tables:       make(map[ca.PID]*processTableImpl),
	}
}

// PageTableImpl is the default implementation of a Page Table
type PageTableImpl struct {
	sync.Mutex
	Log2PageSize uint64
	tables       map[ca.PID]*processTableImpl
	memAllocator MemoryAllocator
	entries      *list.List
	entriesTable map[uint64]*list.Element
}

func (pt *PageTableImpl) PageSize() uint64 {
	return uint64(1) << pt.Log2PageSize
}

func (pt *PageTableImpl) GetMemoryAllocator() MemoryAllocator {
	return pt.memAllocator
}

func (pt *PageTableImpl) AllocateMultiplePages(
	pid ca.PID,
	gpuID uint64,
	vAddr uint64,
	numPages int,
	isUnified bool,
) []Page {
	return pt.memAllocator.AllocateMultiplePageWithGivenVAddr(
		pid,
		int(gpuID),
		vAddr,
		numPages,
		isUnified,
	)
}

func (pt *PageTableImpl) GetAllVirtualPages(
	pid ca.PID,
) []uint64 {
	table := pt.getTable(pid)
	vAddrs := make([]uint64, 0)
	for e := table.entries.Front(); e != nil; e = e.Next() {
		page := e.Value.(Page)

		if page.Valid {
			vAddrs = append(vAddrs, page.VAddr)
		}
	}

	return vAddrs
}

func (pt *PageTableImpl) getTable(pid ca.PID) *processTableImpl {
	pt.Lock()
	defer pt.Unlock()
	table, found := pt.tables[pid]
	if !found {
		table = newProcessTable(pt.Log2PageSize, 4, 9, pt.memAllocator)
		pt.tables[pid] = table
	}

	return table
}

func (pt *PageTableImpl) FindAddr(pid ca.PID, vAddr uint64, level uint64) uint64 {
	table := pt.getTable(pid)
	return table.findAddr(vAddr, level)
}

func (pt *PageTableImpl) AlignToPage(addr uint64) uint64 {
	return (addr >> pt.Log2PageSize) << pt.Log2PageSize
}

func (pt *PageTableImpl) GetVPN(addr uint64) uint64 {
	return addr >> pt.Log2PageSize
}

// Insert put a new page into the PageTable
func (pt *PageTableImpl) Insert(page Page) {
	table := pt.getTable(page.PID)
	// fmt.Println(page.VAddr)
	table.insert(page)
}

// Remove removes the entry in the page table that contains the target
// address.
func (pt *PageTableImpl) Remove(pid ca.PID, vAddr uint64) {
	table := pt.getTable(pid)
	table.remove(vAddr)
}

// Find returns the page that contains the given virtual address. The bool
// return value invicates if the page is found or not.
func (pt *PageTableImpl) Find(pid ca.PID, vAddr uint64) (Page, bool) {
	table := pt.getTable(pid)
	vAddr = pt.AlignToPage(vAddr)
	return table.find(vAddr)
}

// Update changes the field of an existing page. The PID and the VAddr field
// will be used to locate the page to update.
func (pt *PageTableImpl) Update(page Page) {
	table := pt.getTable(page.PID)
	table.update(page)
}

func (pt *PageTableImpl) SetMemoryAllocator(a MemoryAllocator) {
	pt.memAllocator = a
}

func (pt *PageTableImpl) PageTablePagesAsBytes(pid ca.PID) []uint64 {
	table := pt.getTable(pid)
	return table.PageTablePagesAsBytes()
}

func (pt *PageTableImpl) Rearrange(vAddr uint64) uint64 {
	return pt.getTable(0).rearrange(vAddr)
}

func (pt *PageTableImpl) GetRoot(pid ca.PID) uint64 {
	return pt.getTable(pid).root.pAddr
}

func (pt *PageTableImpl) AddOffset(root, vAddr uint64) uint64 {
	return pt.getTable(0).addOffset(root, vAddr)
}

func (pt *PageTableImpl) NextLevel(vAddr uint64) uint64 {
	return pt.getTable(0).nextLevel(vAddr)
}

func (pt *PageTableImpl) MoveToLevel(vAddr uint64, level int) uint64 {
	for i := 0; i < level; i++ {
		vAddr = pt.getTable(0).nextLevel(vAddr)
	}
	return vAddr
}

func (pt *PageTableImpl) MoveFromVAddrToLevel(vAddr uint64, level int) uint64 {
	return pt.MoveToLevel(pt.getTable(0).rearrange(vAddr), level)
}

type processTableImpl struct {
	sync.Mutex
	root             *treeNode
	log2PageSize     uint64
	bitsPerLevel     uint64
	bitsPerLevelMask uint64
	numChildren      uint64
	numLevels        uint64
	memAllocator     MemoryAllocator
	entries          *list.List
	entriesTable     map[uint64]*list.Element
}

type treeNode struct {
	sync.Mutex
	pAddr    uint64
	page     Page
	children []*treeNode
}

func newProcessTable(log2PageSize uint64, numLevels uint64, bitsPerLevel uint64, a MemoryAllocator) *processTableImpl {
	t := new(processTableImpl)
	t.log2PageSize = log2PageSize
	t.numLevels = numLevels
	t.bitsPerLevel = bitsPerLevel
	t.numChildren = uint64(1) << bitsPerLevel
	t.bitsPerLevelMask = ^(^uint64(0) << t.bitsPerLevel)
	t.memAllocator = a
	t.entries = list.New()
	t.entriesTable = make(map[uint64]*list.Element)
	return t
}

func (t *processTableImpl) rearrange(vAddr uint64) uint64 {
	rearrangedVpn := uint64(0)
	vpn := vAddr / (uint64(1) << t.log2PageSize)
	var i uint64 = 0
	for ; i < t.numLevels; i++ {
		temp := vpn & t.bitsPerLevelMask
		rearrangedVpn = (rearrangedVpn << t.bitsPerLevel) | temp
		vpn = vpn >> t.bitsPerLevel
	}
	return rearrangedVpn
}

func (t *processTableImpl) newTreeNode(PID ca.PID, GPUID uint64, vAddr, pAddr uint64, level uint64) *treeNode {
	n := new(treeNode)
	isLeaf := level == t.numLevels-1
	if !isLeaf {
		vAddr = vAddr >> (t.bitsPerLevel*(t.numLevels-level-1) + t.log2PageSize)
		n.pAddr = (t.memAllocator).allocatePageTablePage(PID, int(GPUID), vAddr, pAddr)
		fmt.Println("allocated physical page:", n.pAddr, (((n.pAddr-4096)>>12)%32)/8, level) // this arithmetic is off !!
		n.children = make([]*treeNode, t.numChildren)
	}
	return n
}

func (t *processTableImpl) insert(page Page) {
	t.Lock()
	defer t.Unlock()
	if t.root == nil {
		t.root = t.newTreeNode(page.PID, page.DeviceID, 0, 0, 0)
	}
	n := t.root
	vAddr := t.rearrange(page.VAddr)
	var i uint64 = 0
	for ; i < t.numLevels-1; i++ {
		indexOfChild := vAddr & t.bitsPerLevelMask
		if n.children[indexOfChild] == nil {
			newTreeNode := t.newTreeNode(page.PID, page.DeviceID, page.VAddr, page.PAddr, i)
			n.children[indexOfChild] = newTreeNode
		}
		n = n.children[indexOfChild]
		vAddr = vAddr >> t.bitsPerLevel
	}
	indexOfChild := vAddr & t.bitsPerLevelMask
	if n.children[indexOfChild] != nil {
		panic("page already present")
	}
	n.children[indexOfChild] = t.newTreeNode(page.PID, page.DeviceID, page.VAddr, page.PAddr, i)
	n.children[indexOfChild].pAddr = page.PAddr
	n.children[indexOfChild].page = page
	t.pageMustNotExist(page.VAddr)
	elem := t.entries.PushBack(page)
	t.entriesTable[page.VAddr] = elem
}

func (t *processTableImpl) remove(vAddr uint64) {
	t.Lock()
	defer t.Unlock()
	n := t.root
	var i uint64
	rearrangedVpn := t.rearrange(vAddr)
	for i = 0; i < t.numLevels-1; i++ {
		indexOfChild := rearrangedVpn & t.bitsPerLevelMask
		n = n.children[indexOfChild]
		rearrangedVpn = rearrangedVpn >> t.bitsPerLevel
	}
	indexOfChild := rearrangedVpn & t.bitsPerLevelMask
	if n.children[indexOfChild] == nil {
		panic("virtual page does not exist!")
	}
	if n.children[indexOfChild].page.VAddr != vAddr {
		panic("DFS wrongly implemented")
	}
	n.children[indexOfChild] = nil

	t.pageMustExist(vAddr)
	elem := t.entriesTable[vAddr]
	t.entries.Remove(elem)
	delete(t.entriesTable, vAddr)

}

func (t *processTableImpl) update(page Page) {
	t.Lock()
	defer t.Unlock()
	n := t.root
	vAddr := t.rearrange(page.VAddr)
	var i uint64
	for i = 0; i < t.numLevels; i++ {
		indexOfChild := vAddr & t.bitsPerLevelMask
		n = n.children[indexOfChild]
		vAddr = vAddr >> t.bitsPerLevel
	}
	n.pAddr = page.PAddr
	n.page = page

	t.pageMustExist(page.VAddr)
	elem := t.entriesTable[page.VAddr]
	elem.Value = page

}

func (t *processTableImpl) find(vAddr uint64) (Page, bool) {
	t.Lock()
	defer t.Unlock()

	elem, found := t.entriesTable[vAddr]
	if found {
		return elem.Value.(Page), true
	}

	return Page{}, false
}

func (t *processTableImpl) findAddr(vAddr, level uint64) uint64 {
	if level >= t.numLevels {
		panic("level!")
	}
	n := t.root
	vAddr = t.rearrange(vAddr)
	var i uint64
	for i = 0; i < t.numLevels; i++ {
		indexOfChild := vAddr & t.bitsPerLevelMask
		if i == level {
			if n.children[indexOfChild] == nil {
				panic("oh no!")
			}
			return n.pAddr + indexOfChild*8
		}
		n = n.children[indexOfChild]
		vAddr = vAddr >> t.bitsPerLevel
	}
	panic("boo!")
	return 0
}

func (t *processTableImpl) addOffset(root, vAddr uint64) uint64 {
	// fmt.Println(root, vAddr&t.bitsPerLevelMask, (vAddr&t.bitsPerLevelMask)*8)
	return root + (vAddr&t.bitsPerLevelMask)*8
}

func (t *processTableImpl) nextLevel(vAddr uint64) uint64 {
	return vAddr >> t.bitsPerLevel
}

func (t *processTableImpl) pageMustExist(vAddr uint64) {
	_, found := t.entriesTable[vAddr]
	if !found {
		panic("page does not exist")
	}
}

func (t *processTableImpl) pageMustNotExist(vAddr uint64) {
	_, found := t.entriesTable[vAddr]
	if found {
		panic("page exist")
	}
}

func (t *processTableImpl) PageTablePagesAsBytes() []uint64 {
	pagesAsInts := make([]uint64, 0)
	queue := make([]*treeNode, 0)
	queue = append(queue, t.root)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		numChildren := len(n.children)
		if numChildren > 0 {
			if numChildren != int(t.numChildren) {
				panic("oh no!")
			}
			pagesAsInts = append(pagesAsInts, n.pAddr)
			for i, child := range n.children {
				pAddrOfChild := uint64(0)
				if child != nil {
					pAddrOfChild = child.pAddr
					queue = append(queue, n.children[i])
				}
				pagesAsInts = append(pagesAsInts, pAddrOfChild)
			}
		}
	}
	// fmt.Println(pagesAsInts)
	return pagesAsInts
}

func (pt *PageTableImpl) ProcessNewAllocations(pid ca.PID, newlyAllocatedAddrs []uint64) {
	for _, addr := range newlyAllocatedAddrs {
		pt.recursiveCoalesce(pid, addr, 0)
	}
}

func (pt *PageTableImpl) recursiveCoalesce(pid ca.PID, vAddr uint64, level int) {
	VPN := pt.GetVPN(vAddr)
	page, ok := pt.Find(pid, vAddr)
	if !ok {
		log.Panicf("page not found for VAddr %x", vAddr)
	}
	PFN := page.PAddr >> pt.Log2PageSize
	// 1. 计算当前层级页面组的参数
	// 每一层级，页面组的大小是 8 的 level 次方
	// groupStride 是当前层级一个"单元"包含的 4KB 页面数量
	// Level 0: stride=1 (4KB), Level 1: stride=8 (32KB), Level 2: stride=64 (256KB)
	groupStride := uint64(1) << (3 * level)

	// 计算当前 VPN 所在的 "页面组" (Page Group) 的 Base VPN
	// 通过掩码对齐到当前组的边界
	// Level 0 掩掉低 3 位 (8对齐), Level 1 掩掉低 6 位 (64对齐)
	mask := ^(uint64(8)*groupStride - 1)
	baseVPN := VPN & mask
	basePFN := PFN - (VPN - baseVPN)

	// ==========================================
	// Step 2: 检查物理连续性 (Check Contiguity) [cite: 252, 331]
	// ==========================================

	// 获取 Base PTE (组内第一个 PTE)
	var newValidBits uint8 = 0
	contiguousVPNs := make([]uint64, 0)

	for i := uint64(0); i < 8; i++ {
		// 计算邻居的 VPN
		neighborVPN := baseVPN + (i * groupStride)

		neighborPage, ok := pt.Find(pid, neighborVPN<<pt.Log2PageSize)

		if !ok || neighborPage.SizeBits < uint8(level) {
			continue
		}

		// 检查 3: 物理地址必须连续 (PFN Contiguity)
		// 期望的 PFN = BasePFN + 偏移量
		expectedPFN := basePFN + (i * groupStride)
		neighborPFN := neighborPage.PAddr >> pt.Log2PageSize
		if neighborPFN == expectedPFN {
			newValidBits |= (1 << i)
			contiguousVPNs = append(contiguousVPNs, neighborVPN)
		}
	}
	// ==========================================
	// Step 3: 更新状态 & 尝试晋升 (Update & Promote)
	// ==========================================

	// 更新 Base PTE 的 ValidBits 向量 [cite: 254]
	// 注意：在硬件实现中，这个向量会广播给组内其他 PTE，或者只需更新 Base PTE
	for _, vpn := range contiguousVPNs {
		page, found := pt.Find(pid, vpn<<pt.Log2PageSize)
		if !found {
			log.Panicf("page not found for VPN %d", vpn)
		}

		if page.SizeBits != uint8(level) {
			continue
		}

		pt.updateValidBits(pid, vpn, newValidBits)
	}

	// 如果所有 8 个页面都连续 (ValidBits == 11111111)
	// 触发晋升 (Promotion) [cite: 10, 38]
	if newValidBits == 0xFF {
		// 1. 晋升 Base PTE: SizeBits + 1
		pt.promotePTE(pid, baseVPN, uint8(level+1))

		// 2. 递归: 将这个新晋升的 "大页面" 视为新分配的单元，
		// 在更高层级 (Level + 1) 尝试与更大的邻居合并 [cite: 11, 63]
		pt.recursiveCoalesce(pid, baseVPN<<pt.Log2PageSize, level+1)
	}
}

func (pt *PageTableImpl) updateValidBits(
	pid ca.PID,
	baseVPN uint64,
	newValidBits uint8,
) {
	page, found := pt.Find(pid, baseVPN<<pt.Log2PageSize)
	if !found {
		log.Panicf("base page not found for VPN %d", baseVPN)
	}

	page.ValidBits = newValidBits
	pt.Update(page)
}

func (pt *PageTableImpl) promotePTE(
	pid ca.PID,
	baseVPN uint64,
	newSizeBits uint8,
) {
	page, found := pt.Find(pid, baseVPN<<pt.Log2PageSize)
	if !found {
		log.Panicf("base page not found for VPN %d", baseVPN)
	}

	page.SizeBits = newSizeBits
	pt.Update(page)
}

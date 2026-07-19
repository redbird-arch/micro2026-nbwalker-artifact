package tlb

import (
	"log"

	"gitlab.com/akita/mem/device"
	"gitlab.com/akita/util/ca"
)

type multiLevelMshrImpl struct {
	capacity     int
	entries      []*mshrEntry
	log2PageSize uint64
}

// newMSHR returns a new mshr object
func newMultiLevelMshr(capacity int, log2PageSize uint64) mshr {
	m := new(multiLevelMshrImpl)
	m.capacity = capacity
	m.log2PageSize = log2PageSize
	return m
}

func (m *multiLevelMshrImpl) Add(pid ca.PID, vAddr uint64) *mshrEntry {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			panic("entry already in mshr")
		}
	}

	if len(m.entries) >= m.capacity {
		log.Panic("MSHR is full")
	}

	entry := newMSHREntry()
	entry.pid = pid
	entry.vAddr = vAddr
	m.entries = append(m.entries, entry)
	return entry
}

func (m *multiLevelMshrImpl) Query(pid ca.PID, vAddr uint64) *mshrEntry {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			return e
		}
	}
	return nil
}

func (m *multiLevelMshrImpl) Remove(pid ca.PID, vAddr uint64) *mshrEntry {
	for i, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return e
		}
	}
	panic("trying to remove an non-exist entry")
}

func (m *multiLevelMshrImpl) RemoveEntries(entries []*mshrEntry) {
	for _, entry := range entries {
		found := false

		for i, e := range m.entries {
			if e.pid == entry.pid && e.vAddr == entry.vAddr {
				m.entries = append(m.entries[:i], m.entries[i+1:]...)

				found = true
				break
			}
		}

		if !found {
			panic("trying to remove an non-exist entry")
		}
	}
}

func (m *multiLevelMshrImpl) AllEntries() []*mshrEntry {
	return m.entries
}

func (m *multiLevelMshrImpl) IsFull() bool {
	return len(m.entries) >= m.capacity
}

func (m *multiLevelMshrImpl) Reset() {
	m.entries = nil
}

func (m *multiLevelMshrImpl) GetEntry(pid ca.PID, vAddr uint64) *mshrEntry {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			return e
		}
	}
	return nil
}

func (m *multiLevelMshrImpl) GetEntries(page device.Page) []*mshrEntry {
	mshrEntries := make([]*mshrEntry, 0)

	pid := page.PID
	level := page.SizeBits // 当前返回页面的层级

	// 计算当前页组的覆盖范围掩码
	// 屏蔽低 3*(S+1) 位来确定该层级的 BaseVPN
	// S=0 (32KB 范围) -> 屏蔽 3 位; S=1 (256KB 范围) -> 屏蔽 6 位
	mask := ^(uint64(8)*(uint64(1)<<(3*level)) - 1)

	// 获取返回页面的 BaseVPN
	pteVPN := page.VAddr >> m.log2PageSize
	pteBaseVPN := pteVPN & mask

	for _, e := range m.entries {
		if e.pid != pid {
			continue
		}

		eVPN := e.vAddr >> m.log2PageSize

		if (eVPN & mask) == pteBaseVPN {
			// 2. 计算该请求在页组 8 个插槽（Slots）中的索引
			// 索引由 VPN 的中间 3 位决定
			idx := (eVPN >> (3 * uint64(level))) & 0x7

			// 3. 检查返回页面的有效位向量中，该索引对应的位是否为 1
			if (page.ValidBits & (1 << idx)) != 0 {
				mshrEntries = append(mshrEntries, e)
			}
		}
	}
	return mshrEntries
}

func (m *multiLevelMshrImpl) IsEntryPresent(pid ca.PID, vAddr uint64) bool {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			return true
		}
	}
	return false
}

func (m *multiLevelMshrImpl) IsEntriesPresent(page device.Page) bool {
	pid := page.PID
	level := page.SizeBits // 当前返回页面的层级

	// 计算当前页组的覆盖范围掩码
	// 屏蔽低 3*(S+1) 位来确定该层级的 BaseVPN
	// S=0 (32KB 范围) -> 屏蔽 3 位; S=1 (256KB 范围) -> 屏蔽 6 位
	mask := ^(uint64(8)*(uint64(1)<<(3*level)) - 1)

	// 获取返回页面的 BaseVPN
	pteVPN := page.VAddr >> m.log2PageSize
	pteBaseVPN := pteVPN & mask

	for _, e := range m.entries {
		if e.pid != pid {
			continue
		}

		eVPN := e.vAddr >> m.log2PageSize

		if (eVPN & mask) == pteBaseVPN {
			// 2. 计算该请求在页组 8 个插槽（Slots）中的索引
			// 索引由 VPN 的中间 3 位决定
			idx := (eVPN >> (3 * uint64(level))) & 0x7

			// 3. 检查返回页面的有效位向量中，该索引对应的位是否为 1
			if (page.ValidBits & (1 << idx)) != 0 {
				return true
			}
		}
	}
	return false
}

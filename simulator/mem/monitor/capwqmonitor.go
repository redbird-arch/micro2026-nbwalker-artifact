package monitor

import (
	"log"

	"gitlab.com/akita/akita"
)

type SnapshotReserver struct {
	MaxHP         int // 常量：4
	Total         int // 常量：32
	cooldownCount int // 冷却计数器：记录连续多少个快照 HP 为 0
	lastReserve   int // 记录上一次的预留值
}

type CaPWQMonitor struct {
	*akita.TickingComponent

	L1VCaches []MonitorComponent
	Walkers   []MonitorComponent
	l3TLB     MonitorComponent

	l1vData    []CaPWQMonitorStats
	walkerData []CaPWQMonitorStats
	l3TLBData  CaPWQMonitorStats

	reserver []*SnapshotReserver

	running     bool
	initialized bool

	numEpoches uint64
}

func NewCaPWQMonitor(
	name string,
	engine akita.Engine,
	freq akita.Freq,
) *CaPWQMonitor {
	c := &CaPWQMonitor{}

	c.TickingComponent = akita.NewTickingComponent(
		name, engine, freq, c)

	c.reserver = make([]*SnapshotReserver, 0)
	for i := 0; i < 8; i++ {
		c.reserver = append(c.reserver, &SnapshotReserver{
			MaxHP: 4,
			Total: 32,
		})
	}

	return c
}

func (m *CaPWQMonitor) Tick(now akita.VTimeInSec) bool {
	if !m.running {
		return false
	}

	for _, l1v := range m.L1VCaches {
		m.CollectL1VCacheComponentStats(l1v)
	}

	for _, walker := range m.Walkers {
		m.CollectWalkerComponentStats(walker)
	}

	m.CollectL3TLBComponentStats(m.l3TLB)

	//l1vCacheLength := uint64(0)
	//l1vCacheWalkerLength := uint64(0)
	//walkerReqLength := uint64(0)
	//walkerRspLength := uint64(0)
	//numPTW := uint64(0)
	//
	//for _, stat := range m.l1vData {
	//	l1vCacheLength += stat.L1VLength
	//	l1vCacheWalkerLength += stat.L1VWalkerLength
	//}
	//
	//for _, stat := range m.walkerData {
	//	walkerReqLength += stat.ReqLength
	//	walkerRspLength += stat.RspLength
	//	numPTW += stat.NumPTW
	//}

	//l1VCacheUtilization := float64(l1vCacheLength) / float64(len(m.L1VCaches))
	//L1VCacheWalkerUtilization := float64(l1vCacheWalkerLength) / float64(len(m.L1VCaches))
	//walkerReqUtilization := float64(walkerReqLength) / float64(len(m.Walkers))
	//walkerRspUtilization := float64(walkerRspLength) / float64(len(m.Walkers))
	//avgNumInflightPTW := float64(numPTW) / float64(len(m.Walkers))
	//
	//tracing.StartTask(
	//	"",
	//	"",
	//	now,
	//	m,
	//	"CaPWQMonitor",
	//	"",
	//	StatItem{
	//		NumEpoches:                m.numEpoches,
	//		L1VCacheUtilization:       l1VCacheUtilization,
	//		L1VCacheWalkerUtilization: L1VCacheWalkerUtilization,
	//		WalkerReqUtilization:      walkerReqUtilization,
	//		WalkerRspUtilization:      walkerRspUtilization,
	//		NumInflightPTW:            avgNumInflightPTW,
	//	},
	//)

	m.numEpoches++

	if m.numEpoches == 1 {
		// Reset the stats for the next epoch
		m.l1vData = []CaPWQMonitorStats{}
		m.walkerData = []CaPWQMonitorStats{}
		m.l3TLBData = CaPWQMonitorStats{}

		// 第一个 epoch 主要是为了收集初始状态，暂不调整预留
		log.Printf("Epoch %d: Initial data collected, no reserve adjustment\n", m.numEpoches)
		return true
	}

	if m.UseSoftWalker() {
		for _, l1v := range m.L1VCaches {
			l1v.SentCommand(0)
		}

		m.l3TLB.SentCommand(true)

		// Reset the stats for the next epoch
		m.l1vData = []CaPWQMonitorStats{}
		m.walkerData = []CaPWQMonitorStats{}
		m.l3TLBData = CaPWQMonitorStats{}

		return true
	} else {
		m.l3TLB.SentCommand(false)
	}

	// adaptive control l1v mshr
	numL1VPerGPC := 16
	for i := 0; i < 8; i++ {
		l1VCaches := m.L1VCaches[i*numL1VPerGPC : (i+1)*numL1VPerGPC]

		l1VData := m.l1vData[i*numL1VPerGPC : (i+1)*numL1VPerGPC]
		walkerData := m.walkerData[i]

		totalLength := uint64(0)
		walkLengh := uint64(0)
		for _, stat := range l1VData {
			totalLength += stat.L1VLength - stat.L1VWalkerLength
			walkLengh += stat.L1VWalkerLength
		}

		numInflightPTW := walkerData.NumPTW

		avgWalkerMSHR := float64(numInflightPTW) / float64(numL1VPerGPC)
		avgL1MSHR := (float64(totalLength) / float64(numL1VPerGPC))
		avgL1WalkerMSHR := (float64(walkLengh) / float64(numL1VPerGPC))

		numReserved := m.reserver[i].GetReserveCount(int(avgWalkerMSHR), int(avgL1MSHR))

		for _, l1v := range l1VCaches {
			l1v.SentCommand(numReserved)
		}
		log.Printf("Epoch %d: GPC %d, HP Occupancy %.2f, Normal Occupancy %.2f, Walk Occupancy %.2f, Reserve %d\n",
			m.numEpoches, i, avgWalkerMSHR, avgL1MSHR, avgL1WalkerMSHR, numReserved)
	}

	// Reset the stats for the next epoch
	m.l1vData = []CaPWQMonitorStats{}
	m.walkerData = []CaPWQMonitorStats{}
	m.l3TLBData = CaPWQMonitorStats{}

	return true
}

func (m *CaPWQMonitor) UseSoftWalker() bool {
	numL1VPerGPC := 16
	for i := 0; i < 8; i++ {
		l1VData := m.l1vData[i*numL1VPerGPC : (i+1)*numL1VPerGPC]

		totalLength := uint64(0)
		for _, stat := range l1VData {
			totalLength += stat.L1VLength - stat.L1VWalkerLength
		}

		if m.l3TLBData.NumPTW > 8 && (float64(totalLength)/float64(numL1VPerGPC)) > 24 {
			log.Printf("Epoch %d: L3TLB NumPTW %d, L1MSHR %.2f, Using soft walker\n",
				m.numEpoches, m.l3TLBData.NumPTW, float64(totalLength)/float64(numL1VPerGPC))
			return true
		}
	}

	return false
}

// GetReserveCount 基于快照决定预留
// hpOcc: 快照时 HP 正在使用的数量 (0-4)
// normOcc: 快照时 Normal 正在使用的数量 (0-32)
func (s *SnapshotReserver) GetReserveCount(hpOcc int, normOcc int) int {
	// 1. 基础预留逻辑：HP 只要在用，就必须保护“当前占用 + 1”
	// 加 1 是为了给 HP 留出增长空间，不至于在 1000 cycle 内被锁死
	target := hpOcc + 1
	if target > s.MaxHP {
		target = s.MaxHP
	}

	// 2. 状态更新：如果 HP 此时完全没用 (hpOcc == 0)
	if hpOcc == 0 {
		s.cooldownCount++
	} else {
		s.cooldownCount = 0 // 只要 HP 一出现，立即重置冷却
	}

	// 3. 决定最终预留 (核心自适应点)
	var finalReserve int

	if hpOcc > 0 {
		// 场景 A：HP 活跃，采用“扩张”策略
		// 如果 HP 刚才很忙，现在稍微降了一点，我们也维持高水位，观察一下
		if target < s.lastReserve {
			finalReserve = s.lastReserve // 维持现状，不轻易缩容
		} else {
			finalReserve = target
		}
	} else {
		// 场景 B：HP 快照为 0
		if s.cooldownCount == 1 {
			// 第一个快照为 0：由于是快照，可能只是瞬时空闲，保留 2 个作为缓冲
			finalReserve = 2
		} else if s.cooldownCount == 2 {
			// 连续两个快照为 0：说明 HP 确实不怎么忙，降到 1
			finalReserve = 1
		} else {
			// 连续三个快照（3000 cycle）为 0：彻底释放，设为 0
			// 但如果此时 Normal 也不挤（normOcc < 20），留 1 个也无所谓
			if normOcc > 28 {
				finalReserve = 0
			} else {
				finalReserve = 1
			}
		}
	}

	s.lastReserve = finalReserve
	return finalReserve
}

func (m *CaPWQMonitor) RegisterL1VCache(l1v MonitorComponent) {
	l1v.InitMonitorStats()
	m.L1VCaches = append(m.L1VCaches, l1v)
}

func (m *CaPWQMonitor) RegisterPageWalker(walker MonitorComponent) {
	walker.InitMonitorStats()
	m.Walkers = append(m.Walkers, walker)
}

func (m *CaPWQMonitor) RegisterL3TLB(l3tlb MonitorComponent) {
	l3tlb.InitMonitorStats()
	m.l3TLB = l3tlb
}

func (m *CaPWQMonitor) Start(now akita.VTimeInSec) {
	if !m.initialized {
		for _, l1v := range m.L1VCaches {
			l1v.ClearMonitorStats()
		}

		for _, walker := range m.Walkers {
			walker.ClearMonitorStats()
		}

		m.l3TLB.ClearMonitorStats()

		m.initialized = true
		m.numEpoches = 0

		m.l1vData = []CaPWQMonitorStats{}
		m.walkerData = []CaPWQMonitorStats{}
		m.l3TLBData = CaPWQMonitorStats{}
	}

	m.running = true

	m.TickLater(now)
}

func (m *CaPWQMonitor) Stop() {
	for _, l1v := range m.L1VCaches {
		l1v.ClearMonitorStats()
	}

	for _, walker := range m.Walkers {
		walker.ClearMonitorStats()
	}

	m.l3TLB.ClearMonitorStats()

	m.running = false

}

func (m *CaPWQMonitor) CollectL1VCacheComponentStats(
	component MonitorComponent,
) {
	if m.numEpoches == 0 {
		component.ClearMonitorStats()

		return
	}

	m.l1vData = append(m.l1vData, *component.GetMonitorStats().(*CaPWQMonitorStats))

	component.ClearMonitorStats()
}

func (m *CaPWQMonitor) CollectWalkerComponentStats(
	component MonitorComponent,
) {
	if m.numEpoches == 0 {
		component.ClearMonitorStats()

		return
	}

	m.walkerData = append(m.walkerData, *component.GetMonitorStats().(*CaPWQMonitorStats))

	component.ClearMonitorStats()
}

func (m *CaPWQMonitor) CollectL3TLBComponentStats(
	component MonitorComponent,
) {
	if m.numEpoches == 0 {
		component.ClearMonitorStats()

		return
	}

	m.l3TLBData = *component.GetMonitorStats().(*CaPWQMonitorStats)

	component.ClearMonitorStats()
}

type CaPWQMonitorStats struct {
	Name string

	L1VLength       uint64
	L1VWalkerLength uint64
	ReqLength       uint64
	RspLength       uint64
	NumPTW          uint64
}

func (stat *CaPWQMonitorStats) Clear() {
	stat.L1VLength = 0
	stat.L1VWalkerLength = 0
	stat.ReqLength = 0
	stat.RspLength = 0
	stat.NumPTW = 0
}

type StatItem struct {
	NumEpoches                uint64
	L1VCacheUtilization       float64
	L1VCacheWalkerUtilization float64
	WalkerReqUtilization      float64
	WalkerRspUtilization      float64
	NumInflightPTW            float64
	NumIssuedPTW              float64
}

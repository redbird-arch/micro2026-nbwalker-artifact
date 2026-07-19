package cpu

type CPUData struct {
	Data []byte
}

type CPUStorage struct {
	dataMap map[uint64]*CPUData
}

func NewCPUStorage() *CPUStorage {
	return &CPUStorage{
		dataMap: make(map[uint64]*CPUData),
	}
}

func (s *CPUStorage) Write(addr uint64, data []byte) {
	s.dataMap[addr] = &CPUData{
		Data: data,
	}
}

func (s *CPUStorage) Read(addr uint64) []byte {
	data, found := s.dataMap[addr]
	if !found {
		return nil
	}
	delete(s.dataMap, addr)

	return data.Data
}

func (s *CPUStorage) CheckAddr(addr uint64) bool {
	_, found := s.dataMap[addr]
	return found
}

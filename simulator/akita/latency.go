package akita

import (
	"fmt"
	"strconv"
	"strings"
)

var latencyMatrix [][]uint64

func init() {
	latencyMatrix = [][]uint64{
		{50, 60, 70, 80, 150, 160, 170, 180},
		{60, 70, 80, 50, 160, 170, 180, 150},
		{70, 80, 50, 60, 170, 180, 150, 160},
		{80, 50, 60, 70, 180, 150, 160, 170},
		{150, 160, 170, 180, 50, 60, 70, 80},
		{160, 170, 180, 150, 60, 70, 80, 50},
		{170, 180, 150, 160, 70, 80, 50, 60},
		{180, 150, 160, 170, 80, 50, 60, 70},
	}
}

func GetLatency(
	msg Msg,
) VTimeInSec {
	srcPortName := msg.Meta().Src.Name()
	dstPortName := msg.Meta().Dst.Name()

	if !strings.Contains(srcPortName, "GPC") &&
		!strings.Contains(srcPortName, "MP") {
		return VTimeInSec(50) * GHz.Period()
	}

	if !strings.Contains(dstPortName, "GPC") &&
		!strings.Contains(dstPortName, "MP") {
		return VTimeInSec(50) * GHz.Period()
	}

	var gpcID, mpID, l2ID int
	// identify GPC or MP ID from port name
	if strings.Contains(dstPortName, "MP") {
		gpcID = extractGPCID(srcPortName)
		mpID = extractMPID(dstPortName)
		l2ID = extractL2ID(dstPortName)
	} else {
		mpID = extractMPID(srcPortName)
		l2ID = extractL2ID(srcPortName)
		gpcID = extractGPCID(dstPortName)
	}

	return VTimeInSec(latencyMatrix[gpcID][mpID]+uint64(l2ID)*2) * GHz.Period()
}

func extractGPCID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[2][4:6])
	if err != nil {
		panic(fmt.Sprintf("failed to extract GPC ID from port name %s: %v", portName, err))
	}

	return id
}

func extractMPID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[2][3:5])
	if err != nil {
		panic(fmt.Sprintf("failed to extract MP ID from port name %s: %v", portName, err))
	}

	return id
}

func extractL2ID(portName string) int {
	id, err := strconv.Atoi(strings.Split(portName, ".")[3][3:5])
	if err != nil {
		panic(fmt.Sprintf("failed to extract L2 ID from port name %s: %v", portName, err))
	}

	return id
}

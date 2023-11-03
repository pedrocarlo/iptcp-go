package protocol

import "math/rand"

// Pointers store actual sequence numbers
type TCB struct {
	initialAck uint32
	initialSeq uint32
	currAck    uint32
	currSeq    uint32
	sendBuf    []byte
	// Oldest Unacked Segment
	sendUna uint
	// Next Byte Send
	sendNxt uint
	// Last Byte Write
	sendLbw uint
	recvBuf []byte
	// Next Byte Read
	rcvNxt uint
	// Last Byte Read
	rcvLbRead uint
	// Last Ordered Byte Acked not Read
	rcvFba        uint
	ackedBytesMap map[uint]byte
}

const (
	tcbSize uint16 = 65535
)

// TODO later see best values to initialize sequence numbers
func createTCB() *TCB {
	tcb := new(TCB)
	tcb.recvBuf = make([]byte, uint(tcbSize)+1)
	tcb.sendBuf = make([]byte, uint(tcbSize)+1)
	tcb.ackedBytesMap = map[uint]byte{}
	tcb.initialSeq = rand.Uint32()
	tcb.currSeq = tcb.initialSeq
	return tcb
}

// maybe could have a problem with value being 1 value smaller of real size
func (tcb *TCB) advertiseWindowSize() uint16 {
	// For now return a constant
	return tcbSize
	// return uint16(uint(tcbSize) - ((tcb.rcvNxt - 1) - tcb.rcvLbRead))
}

func wrapIndex(idx uint) uint {
	return idx % uint(tcbSize)
}

// Maybe have a map of acked bytes but not necessarily in order
// What happens when you have to add stuff? Question for prof
func (tcb *TCB) add2Read(seqNum uint, data byte) error {
	tmp := wrapIndex(seqNum)
	// TODO have error case here when you should not add to read
	// MAYBE block add instead of error here
	_, ok := tcb.ackedBytesMap[seqNum]
	if ok {
		return nil
	}
	tcb.recvBuf[tmp] = data
	tcb.ackedBytesMap[seqNum] = data
	if seqNum == tcb.rcvFba+1 {
		tcb.rcvFba = seqNum
	}
	return nil
}

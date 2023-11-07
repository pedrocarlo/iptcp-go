package protocol

import "math/rand"

// Pointers store actual sequence numbers
type TCB struct {
	irs uint32
	iss uint32
	// currAck    uint32
	// currSeq    uint32
	sendBuf []byte
	// Oldest Unacked Segment
	sendUna uint32
	// Next Byte Send
	sendNxt uint32
	// Last Byte Write
	sendLbw uint32
	// Current size of the opposing window
	sendWnd uint16
	rcvBuf  []byte
	// Next Byte Read
	rcvNxt uint32
	// Last Byte Read
	rcvWnd uint16
	// Last Ordered Byte Acked not Read
	rcvFba        uint
	ackedBytesMap map[uint]bool
}

const (
	tcbSize uint = 65535
)

// TODO later see best values to initialize sequence numbers
func createTCB() *TCB {
	tcb := new(TCB)
	tcb.rcvBuf = make([]byte, uint(tcbSize)+1)
	tcb.sendBuf = make([]byte, uint(tcbSize)+1)
	tcb.ackedBytesMap = map[uint]bool{}
	tcb.iss = rand.Uint32()
	tcb.sendUna = tcb.iss
	tcb.sendNxt = tcb.iss + 1
	return tcb
}

func (tcb *TCB) setSynReceivedState(irs uint32, window uint16) {
	tcb.irs = irs
	tcb.rcvNxt = irs + 1
	tcb.sendWnd = window
}

// maybe could have a problem with value being 1 value smaller of real size
func (tcb *TCB) advertiseWindowSize() uint16 {
	// For now return a constant
	return tcb.rcvWnd
}

func wrapIndex(idx uint) uint {
	return idx % (tcbSize + 1)
}

// Maybe have a map of acked bytes but not necessarily in order
// What happens when you have to add stuff? Question for prof
func (tcb *TCB) add2Read(seqNum uint, data byte) error {
	// tmp := wrapIndex(seqNum)
	// TODO have error case here when you should not add to read
	// MAYBE block add instead of error here
	_, ok := tcb.ackedBytesMap[seqNum]
	if ok {
		return nil
	}
	// tcb.recvBuf[tmp] = data
	// tcb.ackedBytesMap[seqNum] = data
	// if seqNum == tcb.rcvFba+1 {
	// 	tcb.rcvFba = seqNum
	// }
	return nil
}

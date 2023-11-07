package protocol

import (
	"math/rand"
	"sync"
)

// Pointers store actual sequence numbers
type TCB struct {
	// Initial Receive Sequence
	irs uint32
	// Initial Send Sequence
	iss     uint32
	sendBuf []byte
	// Oldest Unacked Segment
	sendUna uint32
	// Next Byte Send
	sendNxt uint32
	// Last Byte Read from Send Buffer
	sendLbr uint32
	// Current size of the opposing window
	sendWnd uint16
	rcvBuf  []byte
	// Next Byte Read
	rcvNxt uint32
	// Last Byte Read
	rcvWnd uint16
	// Last Byte Read from Receive Buffer
	rcvLbr            uint32
	ackedBytesMap     map[uint]bool
	windowProbingChan chan bool
	dataToReadSignal  chan bool
	windowRecvSignal  chan bool
	sendMutex         sync.Mutex
	recvMutex         sync.Mutex
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
	tcb.windowProbingChan = make(chan bool)
	tcb.dataToReadSignal = make(chan bool)
	tcb.sendMutex = sync.Mutex{}
	tcb.recvMutex = sync.Mutex{}
	tcb.iss = rand.Uint32()
	tcb.sendUna = tcb.iss
	tcb.sendNxt = tcb.iss + 1
	tcb.sendLbr = tcb.sendNxt
	tcb.rcvWnd = uint16(tcbSize)
	return tcb
}

func (tcb *TCB) setSynReceivedState(irs uint32, window uint16) {
	tcb.irs = irs
	tcb.rcvNxt = irs + 1
	tcb.rcvLbr = tcb.rcvNxt
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

// TODO see how to have it not use 100% cpu here
func (tcb *TCB) add2Send(signalChannel chan bool, payload []byte) {
	// See best approach to send data here, wait to send whole segment or
	// choose a minimum size to send

	// Idle here
	// TODO Bad Design?
	// For now idle
	for tcb.sendWnd < uint16(len(payload)/10) {
	}

	// See if need to just be iterating over this
	// Use windowProbing chan to block when cannot send
	tcb.sendMutex.Lock()
	count := wrapIndex(uint(tcb.sendNxt))
	for _, b := range payload {
		tcb.sendBuf[count] = b
		count = wrapIndex(count + 1)
		tcb.sendNxt++
		tcb.sendWnd--
	}
	tcb.sendMutex.Unlock()
}

// TODO ignoring wrapping around for now
func (tcb *TCB) readFromSend() []byte {
	tcb.sendMutex.Lock()
	// Ignoring wrap around for uint32 nums here
	dist := min(tcb.sendNxt-tcb.sendLbr, uint32(Mss))
	buf := make([]byte, 0)
	minIdx := wrapIndex(uint(tcb.sendLbr) + uint(dist))
	startIdx := wrapIndex(uint(tcb.sendLbr))
	// Wrapped around
	if wrapIndex(uint(tcb.sendLbr)) > minIdx {
		buf = append(buf, tcb.sendBuf[startIdx:]...)
		buf = append(buf, tcb.sendBuf[:minIdx]...)
	} else {
		buf = append(buf, tcb.sendBuf[startIdx:minIdx]...)
	}
	tcb.sendLbr += uint32(len(buf))
	tcb.sendWnd += max(uint16(len(buf)), uint16(tcbSize)-tcb.rcvWnd)
	tcb.sendMutex.Unlock()
	return buf
}

// TODO
// Maybe have a map of acked bytes but not necessarily in order
// What happens when you have to add stuff? Question for prof

// For now no multipl
func (tcb *TCB) add2Read(payload []byte) {

	// TODO
	// Question here on queue the add2Read because multiple calls
	// could be made but recvSignal could activate for another goroutine first?
	for tcb.rcvWnd < uint16(len(payload)/10) {
		<-tcb.windowRecvSignal
	}
	tcb.recvMutex.Lock()
	count := wrapIndex(uint(tcb.rcvNxt))
	for _, b := range payload {
		tcb.rcvBuf[count] = b
		count = wrapIndex(count + 1)
		tcb.rcvNxt++
		tcb.rcvWnd--
	}
	tcb.recvMutex.Unlock()
	// Signaling there is data to read
	tcb.dataToReadSignal <- true
}

func (tcb *TCB) readFromRecv(count uint32) []byte {
	<-tcb.dataToReadSignal
	tcb.recvMutex.Lock()
	// Ignoring wrap around for uint32 nums here
	dist := min(tcb.rcvNxt-tcb.rcvLbr, count)
	println(tcb.rcvNxt - tcb.rcvLbr)
	println(dist)
	buf := make([]byte, 0)
	minIdx := wrapIndex(uint(tcb.rcvLbr) + uint(dist))
	startIdx := wrapIndex(uint(tcb.rcvLbr))
	// Wrapped around
	if wrapIndex(uint(tcb.rcvLbr)) > minIdx {
		buf = append(buf, tcb.rcvBuf[startIdx:]...)
		buf = append(buf, tcb.rcvBuf[:minIdx]...)
	} else {
		buf = append(buf, tcb.rcvBuf[startIdx:minIdx]...)
	}
	tcb.rcvLbr += uint32(len(buf))
	tcb.rcvWnd += max(uint16(len(buf)), uint16(tcbSize)-tcb.rcvWnd)
	tcb.recvMutex.Unlock()
	// Signal here window space was freed
	return buf
}

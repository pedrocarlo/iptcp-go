package protocol

import (
	"math/rand"
	"sync"
)

// TODO TODO Change implementation where pointers numbers are just added to IRS or ISS so they cannot wrap around in a normal connection state
// Use relative addressing like wireshark
// Pointers store actual sequence numbers
type TCB struct {
	// Initial Receive Sequence
	irs uint32
	// Initial Send Sequence
	iss     uint32
	sendBuf []byte
	// Oldest Unacked Segment
	sendUna uint64
	// Next Byte Send
	sendNxt uint64
	// Last Byte Read from Send Buffer
	sendLbr uint64
	// Current size of the opposing window
	sendWnd uint16
	sendWl1 uint32
	sendWl2 uint32
	rcvBuf  []byte
	// Next Byte Read
	rcvNxt uint64
	rcvWnd uint16
	// Last Byte Read from Receive Buffer
	rcvLbr           uint64
	ackedBytesMap    map[uint]bool
	windowSendSignal chan uint16
	dataToSendSignal chan []byte
	windowRecvSignal chan uint16
	dataToRecvSignal chan []byte
	bytesToRead      chan uint
	bytesReadRcv     chan []byte
	bytesToSend      chan uint
	bytesReadSend    chan []byte
	sendMutex        sync.Mutex
	recvMutex        sync.Mutex
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
	tcb.windowSendSignal = make(chan uint16)
	tcb.dataToSendSignal = make(chan []byte)
	tcb.windowRecvSignal = make(chan uint16)
	tcb.dataToRecvSignal = make(chan []byte)
	tcb.bytesToRead = make(chan uint)
	tcb.bytesToSend = make(chan uint)
	tcb.bytesReadRcv = make(chan []byte)
	tcb.bytesReadSend = make(chan []byte)
	tcb.sendMutex = sync.Mutex{}
	tcb.recvMutex = sync.Mutex{}
	tcb.iss = rand.Uint32()
	tcb.sendUna = 0
	tcb.sendNxt = 1
	tcb.sendLbr = tcb.sendNxt
	tcb.rcvWnd = uint16(tcbSize)

	return tcb
}

func (tcb *TCB) initializeControllers() {
	go tcb.tcbReadController()
	// go tcb.tcbReadBufController()
	go tcb.tcbSendController()
	// go tcb.tcbSendBufController()
}

func (tcb *TCB) setSynReceivedState(irs uint32, ackNum uint32, window uint16) {
	tcb.irs = irs
	tcb.sendWl1 = irs
	tcb.sendWl2 = ackNum

	tcb.rcvNxt = 1
	tcb.rcvLbr = tcb.rcvNxt
	tcb.sendWnd = window
}

// maybe could have a problem with value being 1 value smaller of real size
func (tcb *TCB) advertiseWindowSize() uint16 {
	// For now return a constant
	return tcb.rcvWnd
}

// Distance between 2 uint32 num
// Invariant is that numbers can never be ahead of each other more than tcbsize
// As they cannot send more than tcbsize of information
func wrappedDist(num1 uint32, num2 uint32) uint32 {
	if num1 < num2 {
		num1, num2 = num2, num1
	}
	dist := min(num1-num2, uint32(tcbSize))
	return dist
}

func (tcb *TCB) wrapFromIss(pointer uint64) uint32 {
	return uint32(uint64(tcb.iss) + pointer)
}

func (tcb *TCB) wrapFromIrs(pointer uint64) uint32 {
	return uint32(uint64(tcb.irs) + pointer)
}

func wrapIndex(idx uint) uint {
	return idx % (tcbSize + 1)
}

func (tcb *TCB) tcbReadController() {
	for {
		select {
		case payload := <-tcb.dataToRecvSignal:
			for tcb.rcvWnd < uint16(len(payload)) {
				// Wait for some read to clear some buf size
				<-tcb.windowRecvSignal
			}
			tcb.add2Read(payload)
			select {
			case tcb.windowRecvSignal <- tcb.rcvWnd:
			default:
			}
		case bytesToRead := <-tcb.bytesToRead:
			for tcb.rcvWnd == uint16(tcbSize) {
				// Wait for some add to decrease windows size
				<-tcb.windowRecvSignal
			}
			tcb.bytesReadRcv <- tcb.readFromRecv(uint32(bytesToRead))
			select {
			case tcb.windowRecvSignal <- tcb.rcvWnd:
			default:
			}
		}
	}
}

// func (tcb *TCB) tcbReadBufController() {
// 	for {
// 		bytesToRead := <-tcb.bytesToRead
// 		for tcb.rcvWnd == uint16(tcbSize) {
// 			// Wait for some add to decrease windows size
// 			<-tcb.windowRecvSignal
// 		}
// 		tcb.bytesReadRcv <- tcb.readFromRecv(uint32(bytesToRead))
// 		select {
// 		case tcb.windowRecvSignal <- tcb.rcvWnd:
// 		default:
// 		}
// 	}
// }

func (tcb *TCB) tcbSendController() {
	for {
		select {
		case payload := <-tcb.dataToSendSignal:
			for tcb.sendWnd < uint16(len(payload)) {
				// Wait for some read to clear some buf size
				<-tcb.windowSendSignal
			}
			tcb.add2Send(payload)
			select {
			case tcb.windowSendSignal <- tcb.sendWnd:
			default:
			}
		case <-tcb.bytesToSend:
			for tcb.sendWnd == uint16(tcbSize) {
				// Wait for someone to decrease windows size
				<-tcb.windowSendSignal
			}
			tcb.bytesReadSend <- tcb.readFromSend()
			select {
			case tcb.windowSendSignal <- tcb.sendWnd:
			default:
			}
		}
	}
}

// func (tcb *TCB) tcbSendBufController() {
// 	for {
// 		<-tcb.bytesToSend
// 		for tcb.sendWnd == uint16(tcbSize) {
// 			// Wait for some add to decrease windows size
// 			<-tcb.windowSendSignal
// 		}
// 		tcb.bytesReadSend <- tcb.readFromSend()
// 		select {
// 		case tcb.windowSendSignal <- tcb.sendWnd:
// 		default:
// 		}
// 	}
// }

func (tcb *TCB) AddSend(payload []byte) {
	tcb.dataToSendSignal <- payload
}

func (tcb *TCB) add2Send(payload []byte) {
	// See best approach to send data here, wait to send whole segment or
	// choose a minimum size to send

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

func (tcb *TCB) ReadSend() []byte {
	tcb.bytesToSend <- 0
	data := <-tcb.bytesReadSend
	return data
}

func (tcb *TCB) readFromSend() []byte {
	tcb.sendMutex.Lock()
	// Ignoring wrap around for uint32 nums here
	dist := min(tcb.sendNxt-tcb.sendLbr, uint64(Mss))
	buf := make([]byte, 0)
	minIdx := wrapIndex(uint(tcb.sendLbr) + uint(dist))
	startIdx := wrapIndex(uint(tcb.sendLbr))
	// Wrapped around buf
	if wrapIndex(uint(tcb.sendLbr)) > minIdx {
		buf = append(buf, tcb.sendBuf[startIdx:]...)
		buf = append(buf, tcb.sendBuf[:minIdx]...)
	} else {
		buf = append(buf, tcb.sendBuf[startIdx:minIdx]...)
	}
	tcb.sendLbr += uint64(len(buf))
	tcb.sendWnd += max(uint16(len(buf)), uint16(tcbSize)-tcb.sendWnd)
	tcb.sendMutex.Unlock()
	// Signal here window space was freed
	select {
	case tcb.windowRecvSignal <- tcb.rcvWnd:
	default:
	}
	return buf
}

// TODO
// Maybe have a map of acked bytes but not necessarily in order
// What happens when you have to add stuff? Question for prof
func (tcb *TCB) AddRead(payload []byte) {
	tcb.dataToRecvSignal <- payload
}

// For now no wrapping on ack ack num
func (tcb *TCB) add2Read(payload []byte) {
	// TODO
	// Question here on queue the add2Read because multiple calls
	// could be made but recvSignal could activate for another goroutine first?
	tcb.recvMutex.Lock()
	// println("start rcvnxt", tcb.rcvNxt)
	count := wrapIndex(uint(tcb.rcvNxt))
	for _, b := range payload {
		tcb.rcvBuf[count] = b
		count = wrapIndex(count + 1)
		// Inneficient just do one calculation after TODO
		tcb.rcvNxt++
		tcb.rcvWnd--
	}
	// println("end rcvnxt", tcb.rcvNxt)
	tcb.recvMutex.Unlock()
}

func (tcb *TCB) ReadRecv(count uint32) []byte {
	tcb.bytesToRead <- uint(count)
	data := <-tcb.bytesReadRcv
	return data
}

func (tcb *TCB) readFromRecv(count uint32) []byte {
	tcb.recvMutex.Lock()
	// Ignoring wrap around for uint32 nums here
	dist := min(tcb.rcvNxt-tcb.rcvLbr, uint64(count))
	buf := make([]byte, 0)
	minIdx := wrapIndex(uint(tcb.rcvLbr) + uint(dist))
	startIdx := wrapIndex(uint(tcb.rcvLbr))
	// Wrapped around buf
	if wrapIndex(uint(tcb.rcvLbr)) > minIdx {
		buf = append(buf, tcb.rcvBuf[startIdx:]...)
		buf = append(buf, tcb.rcvBuf[:minIdx]...)
	} else {
		buf = append(buf, tcb.rcvBuf[startIdx:minIdx]...)
	}
	tcb.rcvLbr += uint64(len(buf))
	// TODO see if this breaks anything
	tcb.rcvWnd += uint16(len(buf))
	// tcb.rcvWnd += min(uint16(len(buf)), uint16(tcbSize)-tcb.rcvWnd)
	tcb.recvMutex.Unlock()
	// Signal here window space was freed
	select {
	case tcb.windowRecvSignal <- tcb.rcvWnd:
	default:
	}
	return buf
}

package protocol

import (
	"fmt"
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"math/rand"
	"net/netip"
	"sync"
	"time"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
	"github.com/google/netstack/tcpip/header"
)

const (
	// 5 seconds timeout
	timeoutTimeWaitDuration = 5
	ALPHA                   = 0.9
	BETA                    = 1.5
	UBOUND                  = time.Duration(time.Second * 15) // 30 seconds
	LBOUND                  = time.Duration(time.Second * 1)  // 5 seconds
)

type VTcpConn struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan tcpheader.TcpPacket // TODO CHANGE LATER
	status        Status
	tcb           TCB
	signalChannel chan bool
	mutex         sync.Mutex
	timeWaitTimer *time.Timer
	rtoTimer      *time.Timer
	srtt          float64
	queue         rtoQueue
}

/* Start Normal Socket Api */

// TODO Create socket should return an error is there are no sufficent resourceS?
func (d *Device) CreateSocket(remoteAddrPort netip.AddrPort, localPort uint16) *VTcpConn {
	// Create TCB later with a separate call
	return &VTcpConn{
		remoteAddr:    remoteAddrPort,
		localAddr:     netip.AddrPortFrom(d.Interfaces["if0"].Ip, localPort),
		d:             d,
		listenChannel: make(chan tcpheader.TcpPacket),
		status:        Closed,
		signalChannel: make(chan bool),
		mutex:         sync.Mutex{},
		rtoTimer:      nil,
		srtt:          0,
		queue:         make(rtoQueue, 0),
	}
}

func (conn *VTcpConn) initializeTcb() {
	conn.tcb = *createTCB()
	conn.tcb.initializeControllers()
	go conn.windowProbing()
}

// Should I account for a listen socket trying to initiate a connection?
func (d *Device) VConnect(addr netip.Addr, port uint16) (*VTcpConn, error) {
	remoteAddrPort := netip.AddrPortFrom(addr, port)
	// Ports above 20000
	randPort := uint16(rand.Uint32())%(^uint16(0)-20000) + 20000
	localAddrPort := netip.AddrPortFrom(d.Interfaces["if0"].Ip, randPort)
	if !remoteAddrPort.IsValid() {
		return nil, errInvalidIp
	}
	conn := d.CreateSocket(remoteAddrPort, localAddrPort.Port())
	conn.initializeTcb()
	key := SocketKeyFromSocketInterface(conn)
	conn.d.ConnTable[key] = conn
	conn.status = SynSent
	_, err := conn.sendSyn()
	if err != nil {
		return nil, err
	}
	// Wait for SYN + ACK
	for i := 0; i < 4; i++ {
		count := 3 * (i + 1)
		select {
		case <-conn.signalChannel:
			return conn, nil
		case <-time.NewTimer(time.Duration(time.Second * time.Duration(count))).C:
			fmt.Printf("Trying to connect to %s\n", conn.remoteAddr)
		}
	}
	// This will only happen if it times out
	conn.closeDelete()
	return nil, errTimeout
}

// Check rfc for all edge cases
func (conn *VTcpConn) VRead(buf []byte) (int, error) {
	bytesRead := 0
	var err error = nil
	switch conn.status {
	case FinWait1:
		fallthrough
	case FinWait2:
		fallthrough
	case CloseWait:
		fallthrough
	case Established:
		// Handler in the background adds to rcvBuf
		dataRead := conn.tcb.ReadRecv(uint32(len(buf)))
		copy(buf[:len(dataRead)], dataRead)
		bytesRead += len(dataRead)
	default:
		err = errClosing
	}
	return bytesRead, err
}

func (conn *VTcpConn) VWrite(data []byte) (int, error) {
	bytesSent := 0
	var err error = nil
	// Segmenting data to be <= MSS
	i := 0
	for bytesSent < len(data) {
		// println("Bytessent: ", bytesSent, "payload len:", len(data))
		// conn.mutex.Lock()
		cap := conn.tcb.getSendCapacity()
		lastIdx := min(min(int(Mss), len(data)-bytesSent), int(cap))
		// Avoiding sending packages with 0 information inside
		if lastIdx == 0 {
			lastIdx = 1
		}
		// println("i:", i, "last idx: ", lastIdx)
		segData := data[i : i+lastIdx]
		switch conn.status {
		case Established:
			conn.tcb.AddSend(segData)
			// Data send should always be <=Mss size if segData < MSS
			dataSend := conn.tcb.ReadSend(uint(len(segData)))
			n, err := conn.send(
				conn.tcb.wrapFromIss(conn.tcb.sendNxt)-uint32(len(dataSend)),
				conn.tcb.wrapFromIrs(conn.tcb.rcvNxt),
				ACK,
				dataSend)
			bytesSent += n
			if err != nil {
				// conn.mutex.Unlock()
				return bytesSent, err
			}
			i += n
		case CloseWait:
			// Segmentize the buffer and send it with a piggybacked acknowledgment (acknowledgment value = RCV.NXT).
			// If there is insufficient space to remember this buffer, simply return "error: insufficient resources".
			// case TimeWait:
			return 0, errClosing
		default:
			// TODO for now just do this
			// conn.mutex.Unlock()
			return 0, errClosing
		}
		// conn.mutex.Unlock()
	}
	return bytesSent, err
}

func (conn *VTcpConn) VClose() error {
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	wrappedRcvNxt := conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
	var err error = nil
	// TODO Mutex this operation?
	conn.mutex.Lock()
	switch conn.status {
	case SynSent:
		conn.closeDelete()
	case FinWait2:
		err = errClosing
	case TimeWait:
		err = errClosing
	case CloseWait:
		conn.status = LastAck
		conn.tcb.sendNxt++
		_, err = conn.sendFlags(wrappedSendNxt, wrappedRcvNxt, FIN+ACK)
	default:
		conn.status = FinWait1
		conn.tcb.sendNxt++
		_, err = conn.sendFlags(wrappedSendNxt, wrappedRcvNxt, FIN+ACK)
	}
	conn.mutex.Unlock()
	return err
}

func (conn *VTcpConn) closeDelete() {
	conn.status = Closed
	conn.rtoTimer = nil
	conn.timeWaitTimer = nil
	conn.queue = nil
	delete(conn.d.ConnTable, SocketKeyFromSocketInterface(conn))
}

func (conn *VTcpConn) windowProbing() {
	for {
		payload := <-conn.tcb.windowProbeChan
		// println("window probing")
		// println("payload len:", len(payload))
		wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
		wrappedRcvNxt := conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
		packet := conn.CreateTcpPacket(wrappedSendNxt, wrappedRcvNxt, ACK, payload)
		conn.d.SendTcp(conn.remoteAddr.Addr(), packet)
	}
}

func (conn *VTcpConn) timeWaitTimeout() {
	<-conn.timeWaitTimer.C
	conn.mutex.Lock()
	conn.closeDelete()
	conn.mutex.Unlock()
}

func (conn *VTcpConn) startTimeOutTimer() {
	conn.timeWaitTimer = time.NewTimer(time.Second * timeoutTimeWaitDuration)
	go conn.timeWaitTimeout()
}

func (conn *VTcpConn) resetTimeWaitTimer() {
	conn.timeWaitTimer = time.NewTimer(time.Second * timeoutTimeWaitDuration)
}

func (conn *VTcpConn) startRtoTimer() {
	if conn.rtoTimer == nil {
		conn.rtoTimer = time.NewTimer(time.Second * 2)
		go conn.rtoTimeout()
	}
}

func (conn *VTcpConn) rtoTimeout() {
	for {
		if conn.status == Closed {
			return
		}
		<-conn.rtoTimer.C
		items := conn.queue.Items()
		if len(items) > 0 {
			wrappedRcvNxt := conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
			entry := conn.queue.Peek()
			println("retransmitting")
			println("entry seqNUm:", entry.seqNum, "wrappedRcvNxt:", wrappedRcvNxt, "len payload", len(entry.payload))
			packet := conn.CreateTcpPacket(entry.seqNum, wrappedRcvNxt, entry.flags, entry.payload)
			conn.d.SendTcp(conn.remoteAddr.Addr(), packet)
			conn.resetRtoTimer()
		}

	}
}

func (conn *VTcpConn) resetRtoTimer() {
	// conn.rtoTimer = time.NewTimer(min(UBOUND, max(LBOUND, time.Duration((BETA*conn.srtt))*time.Millisecond)))
	conn.rtoTimer = time.NewTimer(time.Second * 10)
}

func (conn *VTcpConn) updateSrtt(entries []*rtoEntry) {
	// var rto time.Duration
	for _, entry := range entries {
		currTime := time.Now()
		// RTT measure in milliseconds
		Rtt := currTime.Sub(entry.start)
		conn.srtt = (ALPHA * conn.srtt) + ((1 - ALPHA) * float64(Rtt.Milliseconds()))
	}
}

// TODO should make this private later
func (conn *VTcpConn) CreateTcpPacket(seqNum uint32, ackNum uint32, flags uint8, payload []byte) tcpheader.TcpPacket {
	hdr := header.TCPFields{
		SrcPort:       conn.localAddr.Port(),
		DstPort:       conn.remoteAddr.Port(),
		SeqNum:        seqNum,
		AckNum:        ackNum,
		DataOffset:    tcpheader.TcpHeaderLen, // TODO see here
		Flags:         flags,
		WindowSize:    conn.tcb.advertiseWindowSize(),
		Checksum:      0,
		UrgentPointer: 0,
	}
	checksum := tcpheader.ComputeTCPChecksum(&hdr, conn.localAddr.Addr(), conn.remoteAddr.Addr(), payload)
	hdr.Checksum = checksum

	return tcpheader.TcpPacket{TcpHdr: hdr, Payload: payload}
}

func (d *Device) SendTcp(dst netip.Addr, tcpPacket tcpheader.TcpPacket) (int, error) {
	// Serialize the TCP header
	tcpHeaderBytes := make(header.TCP, tcpheader.TcpHeaderLen)
	tcpHeaderBytes.Encode(&tcpPacket.TcpHdr)

	// Combine the TCP header + payload into one byte array, which
	// becomes the payload of the IP packet
	ipPacketPayload := make([]byte, 0, len(tcpHeaderBytes)+len(tcpPacket.Payload))
	ipPacketPayload = append(ipPacketPayload, tcpHeaderBytes...)
	ipPacketPayload = append(ipPacketPayload, []byte(tcpPacket.Payload)...)
	n, err := d.SendIP(dst, uint8(tcpheader.IpProtoTcp), ipPacketPayload)
	n -= tcpheader.TcpHeaderLen + ipv4header.HeaderLen
	return n, err
}

func (conn *VTcpConn) GetRemote() netip.AddrPort {
	return conn.remoteAddr
}

func (conn *VTcpConn) GetLocal() netip.AddrPort {
	return conn.localAddr
}

// TODO CHANGE LATER
func (conn *VTcpConn) GetStatus() Status {
	return conn.status
}

// general send
func (conn *VTcpConn) send(seqNum uint32, ackNum uint32, flags uint8, payload []byte) (int, error) {
	packet := conn.CreateTcpPacket(seqNum, ackNum, flags, payload)
	if flags&SYN == SYN || flags&FIN == FIN || len(payload) > 0 {
		entry := &rtoEntry{seqNum: seqNum, flags: flags, payload: payload, start: time.Now()}
		conn.queue.Push(entry)
		conn.startRtoTimer()
	}
	return conn.d.SendTcp(conn.remoteAddr.Addr(), packet)
}

// Just signal sending, no payload
func (conn *VTcpConn) sendFlags(seqNum uint32, ackNum uint32, flags uint8) (int, error) {
	return conn.send(seqNum, ackNum, flags, make([]byte, 0))
}

func (conn *VTcpConn) sendAck(seqNum uint32, ackNum uint32) (int, error) {
	return conn.sendFlags(seqNum, ackNum, ACK)
}

func (conn *VTcpConn) sendSyn() (int, error) {
	return conn.sendFlags(conn.tcb.iss, conn.tcb.irs, SYN)
}

func (conn *VTcpConn) sendRst(ackNum uint32) (int, error) {
	rstPacket := conn.CreateTcpPacket(0, ackNum, RST, make([]byte, 0))
	return conn.d.SendTcp(conn.remoteAddr.Addr(), rstPacket)
}

// Ack is correct if it references values whithin the bounds of ISS and SND.NXT pointer
func (conn *VTcpConn) isAckCorrectBound(ackNum uint32) bool {
	return !(ackNum <= conn.tcb.iss || ackNum > conn.tcb.wrapFromIss(conn.tcb.sendNxt))
}

// SND.UNA < SEG.ACK <= SND.NXT
func (conn *VTcpConn) isAckAcceptable(ackNum uint32) bool {
	// println("sendUNa", conn.tcb.sendUna, "AckNum", ackNum, "SendNxt", conn.tcb.sendNxt)
	wrappedUna := conn.tcb.wrapFromIss(conn.tcb.sendUna)
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	return wrappedUna < ackNum && ackNum <= wrappedSendNxt
}

func (conn *VTcpConn) isSegmentAcceptable(seqNum uint32, lenData uint32) bool {
	wrappedRCVNxt := conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
	if lenData == 0 && conn.tcb.rcvWnd == 0 {
		return seqNum == wrappedRCVNxt
	}
	// TODO this comparison can wrap be careful here
	startSegmentInWnd := wrappedRCVNxt <= seqNum && seqNum < wrappedRCVNxt+uint32(conn.tcb.rcvWnd)
	if lenData == 0 && conn.tcb.rcvWnd > 0 {
		return startSegmentInWnd
	}
	if lenData > 0 && conn.tcb.rcvWnd == 0 {
		return false
	}
	endSegmentNum := seqNum + lenData - 1
	// TODO this comparison can wrap be careful here
	endSegmentInWnd := wrappedRCVNxt <= endSegmentNum && endSegmentNum < wrappedRCVNxt+uint32(conn.tcb.rcvWnd)
	return startSegmentInWnd || endSegmentInWnd
}

/* End Normal Socket Api */

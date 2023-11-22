package protocol

import (
	"fmt"
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
	"time"
)

type VTcpListener struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan KeyPacket // Send remote Addr
	status        Status
}

type KeyPacket struct {
	tcpPacket tcpheader.TcpPacket
	key       SocketKey
}

/* Start Listen Socket Api */

func (d *Device) VListen(port uint16) (*VTcpListener, error) {
	// TODO check if it is okay to use must parse here
	remoteAddrPort := netip.MustParseAddrPort("0.0.0.0:0")
	localAddrPort := netip.MustParseAddrPort(fmt.Sprintf("0.0.0.0:%d", port))
	key := SocketKey{remote: remoteAddrPort, local: localAddrPort, trasportType: tcp}
	// Check in table
	_, ok := d.ListenTable[key]
	if ok {
		return nil, errPortInUse
	}
	ln := &VTcpListener{
		remoteAddr:    remoteAddrPort,
		localAddr:     localAddrPort,
		d:             d,
		listenChannel: make(chan KeyPacket),
		status:        Listen,
	}
	d.ListenTable[key] = ln
	return ln, nil
}

// TODO
// Passive Open
func (ln *VTcpListener) VAccept() (*VTcpConn, error) {
	keyPacket := <-ln.listenChannel
	/* Listen State Specfication SYN BIT Start */
	key, synPacket := keyPacket.key, keyPacket.tcpPacket
	conn := ln.d.CreateSocket(key.remote, key.local.Port())
	conn.initializeTcb()
	conn.tcb.setSynReceivedState(synPacket.TcpHdr.SeqNum, synPacket.TcpHdr.AckNum, synPacket.TcpHdr.WindowSize)
	conn.status = SynRecv
	// Send Syn Ack
	ln.d.ConnTable[key] = conn
	_, err := conn.sendFlags(conn.tcb.iss, conn.tcb.wrapFromIrs(conn.tcb.rcvNxt), SYN+ACK)
	if err != nil {
		return nil, err
	}
	/* Listen State Specfication SYN BIT End */

	// Wait for Ack from channel else timeout
	for i := 0; i < 4; i++ {
		select {
		case <-conn.signalChannel:
			return conn, nil
		case <-time.NewTimer(time.Second * time.Duration(i)).C:
			// return nil, errTimeout
		}
	}
	conn.closeDelete()
	return nil, errTimeout
}

func (ln *VTcpListener) VClose() error {
	ln.d.Mutex.Lock()
	ln.status = Closed
	delete(ln.d.ListenTable, SocketKeyFromSocketInterface(ln))
	ln.d.Mutex.Unlock()
	return nil
}

func (ln *VTcpListener) GetRemote() netip.AddrPort {
	return ln.remoteAddr
}

func (ln *VTcpListener) GetLocal() netip.AddrPort {
	return ln.localAddr
}

func (ln *VTcpListener) GetStatus() Status {
	return ln.status
}

/* End Listen Socket Api */

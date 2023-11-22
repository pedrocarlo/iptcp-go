package utils

import (
	"bufio"
	"errors"
	"fmt"
	protocol "iptcp-pedrocarlo/pkg/protocol"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
)

type Command func(*protocol.Device, []string, *SocketIds)
type CommandMap map[string]Command

var (
	errSocketNotFound = errors.New("socket not found in socket table")
)

// type SocketIds map[uint]protocol.Socket

type SocketIds struct {
	IdToSocketKey map[uint]protocol.SocketKey
	SocketKeyToId map[protocol.SocketKey]uint
	mutex         sync.Mutex
}

func initialize() CommandMap {
	commandMap := make(CommandMap)
	commandMap["exit"] = Exit
	commandMap["li"] = ListInterfaces
	commandMap["ln"] = ListNeighbours
	commandMap["lr"] = ListNeighbours
	commandMap["lr"] = ListRoutes
	commandMap["up"] = UpInterface
	commandMap["down"] = DownInterface
	commandMap["send"] = SendMessage
	commandMap["rip"] = SendTestRip
	commandMap["a"] = ListenPort
	commandMap["c"] = ConnectPort
	commandMap["ls"] = ListSockets
	commandMap["s"] = SendTcp
	commandMap["r"] = ReceiveTcp
	commandMap["cl"] = CloseSocket
	commandMap["sf"] = SendFile

	return commandMap
}

func Repl(d *protocol.Device) {
	commandMap := initialize()

	keys := make([]string, 0, len(commandMap))
	for k := range commandMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	socketIds := new(SocketIds)
	socketIds.IdToSocketKey = make(map[uint]protocol.SocketKey, 0)
	socketIds.SocketKeyToId = make(map[protocol.SocketKey]uint)
	socketIds.mutex = sync.Mutex{}

	repl(d, commandMap, keys, socketIds)
}

func repl(d *protocol.Device, commandMap CommandMap, sortedKeys []string, socketIds *SocketIds) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("> ")
		text, _ := reader.ReadString('\n')
		text = strings.Trim(text, "\n")
		args := strings.Split(text, " ")
		command, ok := commandMap[args[0]]
		if text == "" {
			for _, k := range sortedKeys {
				println(k)
			}
			continue
		}
		if ok {
			command(d, args[1:], socketIds)
		} else {
			fmt.Printf("Command '%s' not found\n", args[0])
		}
	}
}

func Exit(d *protocol.Device, _ []string, _ *SocketIds) {
	os.Exit(0)
}

func ListInterfaces(d *protocol.Device, _ []string, _ *SocketIds) {
	interfaces := d.Interfaces
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "Name\tAddr/Prefix\tState\t")
	for name, inter := range interfaces {
		state := ""
		if inter.IsUp {
			state = "up"
		} else {
			state = "down"
		}
		fmt.Fprintf(w, "%s\t%s/%d\t%s\t\n", name, inter.Ip, inter.Prefix.Bits(), state)
	}
	w.Flush()
}

func ListNeighbours(d *protocol.Device, _ []string, _ *SocketIds) {
	neighbours := d.Neighbours
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "Iface\tVIP\tUDPAddr\t")
	for _, n := range neighbours {
		fmt.Fprintf(w, "%s\t%s\t%s\t\n", n.InterfaceName, n.Ip.String(), n.UdpPort.String())
	}
	w.Flush()
}

func ListRoutes(d *protocol.Device, _ []string, _ *SocketIds) {
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "T\tPrefix\tNext hop\tCost\t")
	// Mutex here stuff
	d.Mutex.Lock()
	for pre, hop := range d.Table {
		var t string
		addr := hop.Addr.String()
		if pre.Bits() == 0 {
			t = "S"
		} else if hop.Cost == 0 {
			t = "L"
			addr = "LOCAL:"
			for iface, inter := range d.Interfaces {
				if inter.Prefix == pre {
					addr += iface
				}
			}
		} else {
			t = "R"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t\n", t, pre, addr, hop.Cost)
	}
	d.Mutex.Unlock()
	w.Flush()
}

func UpInterface(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 1 {
		println("up <ifname>")
		return
	}
	name := args[0]
	d.Mutex.Lock()
	inter, ok := d.Interfaces[name]
	if ok {
		inter.IsUp = true
	}
	d.Mutex.Unlock()
}

func DownInterface(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 1 {
		println("up <ifname>")
		return
	}
	name := args[0]
	d.Mutex.Lock()
	inter, ok := d.Interfaces[name]
	if ok {
		inter.IsUp = false
	}
	d.Mutex.Unlock()
}

func SendMessage(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 2 {
		println("send <addr> <message>")
		return
	}
	addr, msg := args[0], args[1]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		fmt.Println(err)
		return
	}
	n, _ := d.SendIP(addrIp, 0, []byte(msg))
	fmt.Printf("Sent %d bytes\n", n)
}

func SendTestRip(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 1 {
		println("usage: a <port>")
		return
	}
	addr := args[0]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		fmt.Printf("Sent %d bytes\n", 0)
		return
	}
	h, err := d.CreateRipPacket(2, addrIp, d.Table)
	if err != nil {
		fmt.Printf("Sent %d bytes\n", 0)
		return
	}
	h.PrintHeader()
	marshal, err := h.Marshal()
	if err != nil {
		fmt.Printf("Sent %d bytes\n", 0)
		return
	}
	n, _ := d.SendIP(addrIp, 200, marshal)
	fmt.Printf("Sent %d bytes\n", n)
}

func ListenPort(d *protocol.Device, args []string, socketIds *SocketIds) {
	go listenPort(d, args, socketIds)
}

// Update Repl table
func listenPort(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 1 {
		println("a <port>")
		return
	}
	portStr := args[0]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		println(err.Error())
		return
	}
	ln, err := d.VListen(uint16(port))
	if err != nil {
		println(err.Error())
		return
	}
	updataSocketIds(d, socketIds)
	id := socketIds.SocketKeyToId[protocol.SocketKeyFromSocketInterface(ln)]
	fmt.Printf("Created listen socket with ID %d\n", id)
	for {
		// Just listen and accept
		conn, err := ln.VAccept()
		if err != nil {
			continue
		}
		// Debugging
		updataSocketIds(d, socketIds)
		newId := socketIds.SocketKeyToId[protocol.SocketKeyFromSocketInterface(conn)]
		fmt.Printf("New connection on socket %d => created new socket %d\n", id, newId)
	}
}

// Update repl Table
func ConnectPort(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 2 {
		println("usage: c <vip> <port>")
		return
	}
	addr, portStr := args[0], args[1]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		fmt.Println(err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Println(err)
		return
	}
	conn, err := d.VConnect(addrIp, uint16(port))
	if err != nil {
		fmt.Println(err)
		return
	}
	updataSocketIds(d, socketIds)
	id := socketIds.SocketKeyToId[protocol.SocketKeyFromSocketInterface(conn)]
	fmt.Printf("Created new socket with ID %d\n", id)
}

func ListSockets(d *protocol.Device, _ []string, socketIds *SocketIds) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprint(w, "\n")
	fmt.Fprintf(w, "SID\tLAddr\tLPort\tRAddr\tRPort\tStatus\t\n")

	updataSocketIds(d, socketIds)

	keys := make([]uint, 0, len(socketIds.IdToSocketKey))
	for k := range socketIds.IdToSocketKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	for _, id := range keys {
		key := socketIds.IdToSocketKey[id]
		var socket protocol.Socket
		socket, ok := d.ListenTable[key]
		if !ok {
			socket, ok = d.ConnTable[key]
			if !ok {
				println(errSocketNotFound)
			}
		}
		remoteAddrPort := socket.GetRemote()
		localAddrPort := socket.GetLocal()
		fmt.Fprintf(
			w,
			"%d\t%s\t%d\t%s\t%d\t%s\t\n",
			id,
			localAddrPort.Addr(),
			localAddrPort.Port(),
			remoteAddrPort.Addr(),
			remoteAddrPort.Port(),
			protocol.GetSocketStatusStr(socket))
	}
	w.Flush()
}

// TODO block commands from executing when not in established state
func SendTcp(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 2 {
		println("usage: s <socket ID> <bytes>")
		return
	}
	socketIdStr, payload := args[0], args[1]
	conn, err := getSocket(socketIdStr, d, socketIds)
	if err != nil {
		fmt.Println(err)
		return
	}
	n, err := conn.VWrite([]byte(payload))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("Wrote %d bytes\n", n)
}

// TODO block commands from executing when not in established state
func ReceiveTcp(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 2 {
		println("usage: r <socketID> <numbytes>")
		return
	}
	socketIdStr, numBytesStr := args[0], args[1]
	conn, err := getSocket(socketIdStr, d, socketIds)
	if err != nil {
		fmt.Println(err)
		return
	}
	numBytes, err := strconv.Atoi(numBytesStr)
	// Throw error if num is negative
	if err != nil {
		fmt.Println(err)
		return
	}
	buf := make([]byte, numBytes)
	n, err := conn.VRead(buf)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("Read %d bytes: %s\n", n, buf[:n])
}

func CloseSocket(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 1 {
		println("usage: cl <socket ID>")
		return
	}
	socketIdStr := args[0]
	conn, err := getSocket(socketIdStr, d, socketIds)
	if err == nil {
		err = conn.VClose()
		if err != nil {
			fmt.Println(err)
			return
		}
		updataSocketIds(d, socketIds)
	}
	ln, err2 := getListenSocket(socketIdStr, d, socketIds)
	if err2 == nil {
		err = ln.VClose()
		if err != nil {
			fmt.Println(err)
			return
		}
		updataSocketIds(d, socketIds)
	}
	if err != nil {
		fmt.Println(err)
	}
}

func SendFile(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 3 {
		println("usage: sf <file path> <addr> <port>")
		return
	}
	go sendFile(d, args, socketIds)
}

func sendFile(d *protocol.Device, args []string, socketIds *SocketIds) {
	filePath, addr, portStr := args[0], args[1], args[2]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		fmt.Println(err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Println(err)
		return
	}
	conn, err := d.VConnect(addrIp, uint16(port))
	if err != nil {
		fmt.Println(err)
		return
	}
	payload, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println(err)
		return
	}
	// payload := make([]byte, ^uint16(0))
	// for i := uint(0); i < uint(^uint16(0)); i++ {
	// 	payload = append(payload)
	// }
	println("sending")
	n, err := conn.VWrite(payload)
	if err != nil {
		fmt.Println(err)
		return
	}
	println("sent")
	fmt.Printf("Sent %d total bytes\n", n)
	if conn.VClose() != nil {
		fmt.Println(err)
	}
}

func getSocket(idStr string, d *protocol.Device, socketIds *SocketIds) (*protocol.VTcpConn, error) {
	socketId, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, err
	}
	socketKey, ok := socketIds.IdToSocketKey[uint(socketId)]
	if !ok {
		return nil, errSocketNotFound
	}
	conn, ok := d.ConnTable[socketKey]
	if !ok {
		return nil, errSocketNotFound
	}
	return conn, nil
}

func getListenSocket(idStr string, d *protocol.Device, socketIds *SocketIds) (*protocol.VTcpListener, error) {
	socketId, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, err
	}
	socketKey, ok := socketIds.IdToSocketKey[uint(socketId)]
	if !ok {
		return nil, errSocketNotFound
	}
	ln, ok := d.ListenTable[socketKey]
	if !ok {
		return nil, errSocketNotFound
	}
	return ln, nil
}

func updataSocketIds(d *protocol.Device, socketIds *SocketIds) {
	var newIdToSocketKey map[uint]protocol.SocketKey
	var newSocketKeyToId map[protocol.SocketKey]uint
	count := uint(0)
	allSockets := make(map[protocol.SocketKey]protocol.Socket)
	newIdToSocketKey = make(map[uint]protocol.SocketKey)
	newSocketKeyToId = make(map[protocol.SocketKey]uint)

	addAllSocketsToMap(d, allSockets)

	// Maintain existing sockets
	for key, id := range socketIds.SocketKeyToId {
		_, ok := allSockets[key]
		// Purge id and key if cannot find it anywhere in table
		// socket api has its own housekeeping with closed sockets
		// that it should remove
		if !ok {
			delete(socketIds.IdToSocketKey, id)
			delete(socketIds.SocketKeyToId, key)
		} else {

			// Copy previous info to maintain ids
			newIdToSocketKey[id] = key
			newSocketKeyToId[key] = id
		}
	}
	// Add new sockets not in map already
	for key := range allSockets {
		newOk := true
		// Add it to some index not already in map
		// maybe O(N^2). Try to see a better way to do this
		_, ok := newSocketKeyToId[key]
		// Check if key already in maps
		if ok {
			count++
			continue
		}
		for newOk {
			_, ok := newIdToSocketKey[count]
			newOk = ok
			if !newOk {
				newSocketKeyToId[key] = count
				newIdToSocketKey[count] = key
			}
			count++
		}
	}
	socketIds.IdToSocketKey = newIdToSocketKey
	socketIds.SocketKeyToId = newSocketKeyToId
}

func addAllSocketsToMap(d *protocol.Device, allSockets map[protocol.SocketKey]protocol.Socket) {
	for key, socket := range d.ListenTable {
		allSockets[key] = socket
	}
	for key, socket := range d.ConnTable {
		allSockets[key] = socket
	}
}

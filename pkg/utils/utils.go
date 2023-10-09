package utils

import (
	"bufio"
	"fmt"
	protocol "iptcp-pedrocarlo/pkg"
	"net/netip"
	"os"
	"strings"
)

func Repl(d *protocol.Device) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("> ")
		text, _ := reader.ReadString('\n')
		text = strings.Trim(text, "\n")
		if text == "q" {
			os.Exit(0)
		}
		if text == "li" {
			ListInterfaces(d)
		}
		if text == "ln" {
			ListNeighbours(d)
		}
		splitText := strings.Split(text, " ")

		if splitText[0] == "up" {
			if len(splitText) < 2 {
				println("Should pass more arguments for command")
				continue
			}
			UpInterface(d, splitText[1])
		}
		if splitText[0] == "down" {
			if len(splitText) < 2 {
				println("Should pass more arguments for command")
				continue
			}
			DownInterface(d, splitText[1])
		}
		if splitText[0] == "send" {
			if len(splitText) < 3 {
				println("Should pass more arguments for command")
				continue
			}
			n, err := SendMessage(d, splitText[1], strings.Join(splitText[2:], " "))
			if err != nil {
				println(err.Error())
			}
			fmt.Printf("sent %d bytes\n", n)
		}

	}
}

func ListInterfaces(d *protocol.Device) {
	interfaces := d.Interfaces
	fmt.Printf("Name Addr/Prefix State\n")
	for name, inter := range interfaces {
		state := ""
		if inter.IsUp {
			state = "up"
		} else {
			state = "down"
		}
		fmt.Printf("%s %s/%d %s\n", name, inter.Ip, inter.Prefix.Bits(), state)
	}
}

func ListNeighbours(d *protocol.Device) {
	neighbours := d.Neighbours
	fmt.Printf("Iface VIP UDPAddr\n")
	for _, n := range neighbours {
		fmt.Printf("%s %s %s\n", n.InterfaceName, n.Ip.String(), n.UdpPort.String())
	}
}

func ListRoutes(d *protocol.Device) {

}

func UpInterface(d *protocol.Device, name string) {
	d.Interfaces[name].IsUp = true
}

func DownInterface(d *protocol.Device, name string) {
	d.Interfaces[name].IsUp = false
}

func SendMessage(d *protocol.Device, addr string, msg string) (int, error) {
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, err
	}
	n, err := d.SendIP(addrIp, 0, []byte(msg))
	return n, err
}

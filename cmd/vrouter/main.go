package vrouter

import ipv4header "github.com/brown-csci1680/iptcp-headers"

func main() {
	x := ipv4header.IPv4Header{}
	println(x.Checksum)
}

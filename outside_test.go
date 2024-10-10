package nebula

import (
	"net"
	"net/netip"
	"testing"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"

	"github.com/slackhq/nebula/firewall"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/ipv4"
)

func Test_newPacket(t *testing.T) {
	p := &firewall.Packet{}

	// length fails
	err := newPacket([]byte{}, true, p)
	assert.EqualError(t, err, "packet too short")

	err = newPacket([]byte{0x40}, true, p)
	assert.EqualError(t, err, "ipv4 packet is less than 20 bytes")

	err = newPacket([]byte{0x60}, true, p)
	assert.EqualError(t, err, "ipv6 packet is less than 20 bytes")

	// length fail with ip options
	h := ipv4.Header{
		Version: 1,
		Len:     100,
		Src:     net.IPv4(10, 0, 0, 1),
		Dst:     net.IPv4(10, 0, 0, 2),
		Options: []byte{0, 1, 0, 2},
	}

	b, _ := h.Marshal()
	err = newPacket(b, true, p)

	assert.EqualError(t, err, "ipv4 packet is less than 28 bytes, ip header len: 24")

	// not an ipv4 packet
	err = newPacket([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true, p)
	assert.EqualError(t, err, "packet is an unknown ip version: 0")

	// invalid ihl
	err = newPacket([]byte{4<<4 | (8 >> 2 & 0x0f), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true, p)
	assert.EqualError(t, err, "ipv4 packet had an invalid header length: 8")

	// account for variable ip header length - incoming
	h = ipv4.Header{
		Version:  1,
		Len:      100,
		Src:      net.IPv4(10, 0, 0, 1),
		Dst:      net.IPv4(10, 0, 0, 2),
		Options:  []byte{0, 1, 0, 2},
		Protocol: firewall.ProtoTCP,
	}

	b, _ = h.Marshal()
	b = append(b, []byte{0, 3, 0, 4}...)
	err = newPacket(b, true, p)

	assert.Nil(t, err)
	assert.Equal(t, p.Protocol, uint8(firewall.ProtoTCP))
	assert.Equal(t, p.LocalIP, netip.MustParseAddr("10.0.0.2"))
	assert.Equal(t, p.RemoteIP, netip.MustParseAddr("10.0.0.1"))
	assert.Equal(t, p.RemotePort, uint16(3))
	assert.Equal(t, p.LocalPort, uint16(4))

	// account for variable ip header length - outgoing
	h = ipv4.Header{
		Version:  1,
		Protocol: 2,
		Len:      100,
		Src:      net.IPv4(10, 0, 0, 1),
		Dst:      net.IPv4(10, 0, 0, 2),
		Options:  []byte{0, 1, 0, 2},
	}

	b, _ = h.Marshal()
	b = append(b, []byte{0, 5, 0, 6}...)
	err = newPacket(b, false, p)

	assert.Nil(t, err)
	assert.Equal(t, p.Protocol, uint8(2))
	assert.Equal(t, p.LocalIP, netip.MustParseAddr("10.0.0.1"))
	assert.Equal(t, p.RemoteIP, netip.MustParseAddr("10.0.0.2"))
	assert.Equal(t, p.RemotePort, uint16(6))
	assert.Equal(t, p.LocalPort, uint16(5))
}

func Test_newPacket_v6(t *testing.T) {
	p := &firewall.Packet{}

	ip := layers.IPv6{
		Version:    6,
		NextHeader: firewall.ProtoUDP,
		HopLimit:   128,
		SrcIP:      net.IPv6linklocalallrouters,
		DstIP:      net.IPv6linklocalallnodes,
	}

	udp := layers.UDP{
		SrcPort: layers.UDPPort(36123),
		DstPort: layers.UDPPort(22),
	}
	err := udp.SetNetworkLayerForChecksum(&ip)
	if err != nil {
		panic(err)
	}

	buffer := gopacket.NewSerializeBuffer()
	opt := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	err = gopacket.SerializeLayers(buffer, opt, &ip, &udp, gopacket.Payload([]byte{0xde, 0xad, 0xbe, 0xef}))
	if err != nil {
		panic(err)
	}
	b := buffer.Bytes()

	//test incoming
	err = newPacket(b, true, p)

	assert.Nil(t, err)
	assert.Equal(t, p.Protocol, uint8(firewall.ProtoUDP))
	assert.Equal(t, p.RemoteIP, netip.MustParseAddr("ff02::2"))
	assert.Equal(t, p.LocalIP, netip.MustParseAddr("ff02::1"))
	assert.Equal(t, p.RemotePort, uint16(36123))
	assert.Equal(t, p.LocalPort, uint16(22))

	//test outgoing
	err = newPacket(b, false, p)

	assert.Nil(t, err)
	assert.Equal(t, p.Protocol, uint8(firewall.ProtoUDP))
	assert.Equal(t, p.LocalIP, netip.MustParseAddr("ff02::2"))
	assert.Equal(t, p.RemoteIP, netip.MustParseAddr("ff02::1"))
	assert.Equal(t, p.LocalPort, uint16(36123))
	assert.Equal(t, p.RemotePort, uint16(22))
}

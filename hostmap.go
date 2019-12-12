package nebula

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rcrowley/go-metrics"
	"github.com/sirupsen/logrus"
	"github.com/slackhq/nebula/cert"
)

//const ProbeLen = 100
const PromoteEvery = 1000
const MaxRemotes = 10

// How long we should prevent roaming back to the previous IP.
// This helps prevent flapping due to packets already in flight
const RoamingSupressSeconds = 2

// The total Nebula packet overhead is:
// - HeaderLen bytes for the Nebula header.
// - 16 bytes for the encryption cipher's AEAD 128-bit tag.
//     NOTE: both AESGCM and ChaChaPoly have a 16 byte tag, but if we add other
//       ciphers in the future we could calculate this based on the cipher,
//       returned by (cipher.AEAD).Overhead().
// - 20 bytes for our IPv4 header.
//     (max is 60 bytes, but we don't use IPv4 options)
//     TODO: Could routers along the path inject a larger IPv4 header? If so,
//       we may need to increase this.
// - 8 bytes for our UDP header.
const NebulaHeaderOverhead = HeaderLen + 16 + 20 + 8

// TODO make configurable
// NOTE: This is only used when the experimental `tun.path_mtu_discovery`
// feature is enabled
const MTUTimeoutSeconds = 60

type HostMap struct {
	sync.RWMutex    //Because we concurrently read and write to our maps
	name            string
	Indexes         map[uint32]*HostInfo
	Hosts           map[uint32]*HostInfo
	preferredRanges []*net.IPNet
	vpnCIDR         *net.IPNet
	defaultRoute    uint32
}

type HostInfo struct {
	remote            *HostInfoDest
	Remotes           []*HostInfoDest
	promoteCounter    uint32
	ConnectionState   *ConnectionState
	handshakeStart    time.Time
	HandshakeReady    bool
	HandshakeCounter  int
	HandshakeComplete bool
	HandshakePacket   map[uint8][]byte
	packetStore       []*cachedPacket
	remoteIndexId     uint32
	localIndexId      uint32
	hostId            uint32
	recvError         int

	lastRoam       time.Time
	lastRoamRemote *HostInfoDest
}

type cachedPacket struct {
	messageType    NebulaMessageType
	messageSubType NebulaMessageSubType
	callback       packetCallback
	packet         []byte
}

type packetCallback func(t NebulaMessageType, st NebulaMessageSubType, h *HostInfo, p, nb, out []byte)

type HostInfoDest struct {
	addr *udpAddr
	//probes       [ProbeLen]bool
	probeCounter int

	// The discovered mtu to use for the chosen remote.
	MTU          int
	MTUTimestamp time.Time
}

type Probe struct {
	Addr    *net.UDPAddr
	Counter int
}

func NewHostMap(name string, vpnCIDR *net.IPNet, preferredRanges []*net.IPNet) *HostMap {
	h := map[uint32]*HostInfo{}
	i := map[uint32]*HostInfo{}
	m := HostMap{
		name:            name,
		Indexes:         i,
		Hosts:           h,
		preferredRanges: preferredRanges,
		vpnCIDR:         vpnCIDR,
		defaultRoute:    0,
	}
	return &m
}

// UpdateStats takes a name and reports host and index counts to the stats collection system
func (hm *HostMap) EmitStats(name string) {
	hm.RLock()
	hostLen := len(hm.Hosts)
	indexLen := len(hm.Indexes)
	hm.RUnlock()

	metrics.GetOrRegisterGauge("hostmap."+name+".hosts", nil).Update(int64(hostLen))
	metrics.GetOrRegisterGauge("hostmap."+name+".indexes", nil).Update(int64(indexLen))
}

func (hm *HostMap) GetIndexByVpnIP(vpnIP uint32) (uint32, error) {
	hm.RLock()
	if i, ok := hm.Hosts[vpnIP]; ok {
		index := i.localIndexId
		hm.RUnlock()
		return index, nil
	}
	hm.RUnlock()
	return 0, errors.New("vpn IP not found")
}

func (hm *HostMap) GetVpnIPByIndex(index uint32) (uint32, error) {
	hm.RLock()
	if i, ok := hm.Indexes[index]; ok {
		vpnIP := i.hostId
		hm.RUnlock()
		return vpnIP, nil
	}
	hm.RUnlock()
	return 0, errors.New("vpn IP not found")
}

func (hm *HostMap) Add(ip uint32, hostinfo *HostInfo) {
	hm.Lock()
	hm.Hosts[ip] = hostinfo
	hm.Unlock()
}

func (hm *HostMap) AddVpnIP(vpnIP uint32) *HostInfo {
	h := &HostInfo{}
	hm.RLock()
	if _, ok := hm.Hosts[vpnIP]; !ok {
		hm.RUnlock()
		h = &HostInfo{
			Remotes:         []*HostInfoDest{},
			promoteCounter:  0,
			hostId:          vpnIP,
			HandshakePacket: make(map[uint8][]byte, 0),
		}
		hm.Lock()
		hm.Hosts[vpnIP] = h
		hm.Unlock()
		return h
	} else {
		h = hm.Hosts[vpnIP]
		hm.RUnlock()
		return h
	}
}

func (hm *HostMap) DeleteVpnIP(vpnIP uint32) {
	hm.Lock()
	delete(hm.Hosts, vpnIP)
	if len(hm.Hosts) == 0 {
		hm.Hosts = map[uint32]*HostInfo{}
	}
	hm.Unlock()

	if l.Level >= logrus.DebugLevel {
		l.WithField("hostMap", m{"mapName": hm.name, "vpnIp": IntIp(vpnIP), "mapTotalSize": len(hm.Hosts)}).
			Debug("Hostmap vpnIp deleted")
	}
}

func (hm *HostMap) AddIndex(index uint32, ci *ConnectionState) (*HostInfo, error) {
	hm.Lock()
	if _, ok := hm.Indexes[index]; !ok {
		h := &HostInfo{
			ConnectionState: ci,
			Remotes:         []*HostInfoDest{},
			localIndexId:    index,
			HandshakePacket: make(map[uint8][]byte, 0),
		}
		hm.Indexes[index] = h
		l.WithField("hostMap", m{"mapName": hm.name, "indexNumber": index, "mapTotalSize": len(hm.Indexes),
			"hostinfo": m{"existing": false, "localIndexId": h.localIndexId, "hostId": IntIp(h.hostId)}}).
			Debug("Hostmap index added")

		hm.Unlock()
		return h, nil
	}
	hm.Unlock()
	return nil, fmt.Errorf("refusing to overwrite existing index: %d", index)
}

func (hm *HostMap) AddIndexHostInfo(index uint32, h *HostInfo) {
	hm.Lock()
	h.localIndexId = index
	hm.Indexes[index] = h
	hm.Unlock()

	if l.Level > logrus.DebugLevel {
		l.WithField("hostMap", m{"mapName": hm.name, "indexNumber": index, "mapTotalSize": len(hm.Indexes),
			"hostinfo": m{"existing": true, "localIndexId": h.localIndexId, "hostId": IntIp(h.hostId)}}).
			Debug("Hostmap index added")
	}
}

func (hm *HostMap) AddVpnIPHostInfo(vpnIP uint32, h *HostInfo) {
	hm.Lock()
	h.hostId = vpnIP
	hm.Hosts[vpnIP] = h
	hm.Unlock()

	if l.Level > logrus.DebugLevel {
		l.WithField("hostMap", m{"mapName": hm.name, "vpnIp": IntIp(vpnIP), "mapTotalSize": len(hm.Hosts),
			"hostinfo": m{"existing": true, "localIndexId": h.localIndexId, "hostId": IntIp(h.hostId)}}).
			Debug("Hostmap vpnIp added")
	}
}

func (hm *HostMap) DeleteIndex(index uint32) {
	hm.Lock()
	delete(hm.Indexes, index)
	if len(hm.Indexes) == 0 {
		hm.Indexes = map[uint32]*HostInfo{}
	}
	hm.Unlock()

	if l.Level >= logrus.DebugLevel {
		l.WithField("hostMap", m{"mapName": hm.name, "indexNumber": index, "mapTotalSize": len(hm.Indexes)}).
			Debug("Hostmap index deleted")
	}
}

func (hm *HostMap) QueryIndex(index uint32) (*HostInfo, error) {
	//TODO: we probably just want ot return bool instead of error, or at least a static error
	hm.RLock()
	if h, ok := hm.Indexes[index]; ok {
		hm.RUnlock()
		return h, nil
	} else {
		hm.RUnlock()
		return nil, errors.New("unable to find index")
	}
}

// This function needs to range because we don't keep a map of remote indexes.
func (hm *HostMap) QueryReverseIndex(index uint32) (*HostInfo, error) {
	hm.RLock()
	for _, h := range hm.Indexes {
		if h.ConnectionState != nil && h.remoteIndexId == index {
			hm.RUnlock()
			return h, nil
		}
	}
	for _, h := range hm.Hosts {
		if h.ConnectionState != nil && h.remoteIndexId == index {
			hm.RUnlock()
			return h, nil
		}
	}
	hm.RUnlock()
	return nil, fmt.Errorf("unable to find reverse index or connectionstate nil in %s hostmap", hm.name)
}

// This function needs to range because we don't keep a map of remote IPs
// TODO: maintain a map of remoteIP -> HostInfo in the HostMap.
// Returns a slice since mulitple "hosts" could have the same remote IP (different ports)
func (hm *HostMap) QueryRemoteIP(remoteNoPort *udpAddr) []*HostInfo {
	hm.RLock()

	var hosts []*HostInfo

	for _, h := range hm.Hosts {

		for _, r := range h.Remotes {
			if r != nil && r.addr.IPEquals(remoteNoPort) {
				hosts = append(hosts, h)
				break
			}
		}
	}
	hm.RUnlock()
	return hosts
}

func (hm *HostMap) AddRemote(vpnIp uint32, remote *udpAddr) *HostInfo {
	hm.Lock()
	i, v := hm.Hosts[vpnIp]
	if v {
		i.AddRemote(*remote)
	} else {
		i = &HostInfo{
			Remotes:         []*HostInfoDest{NewHostInfoDest(remote)},
			promoteCounter:  0,
			hostId:          vpnIp,
			HandshakePacket: make(map[uint8][]byte, 0),
		}
		i.setRemote(i.Remotes[0])
		hm.Hosts[vpnIp] = i
		l.WithField("hostMap", m{"mapName": hm.name, "vpnIp": IntIp(vpnIp), "udpAddr": remote, "mapTotalSize": len(hm.Hosts)}).
			Debug("Hostmap remote ip added")
	}
	i.ForcePromoteBest(hm.preferredRanges)
	hm.Unlock()
	return i
}

func (hm *HostMap) QueryVpnIP(vpnIp uint32) (*HostInfo, error) {
	return hm.queryVpnIP(vpnIp, nil)
}

// PromoteBestQueryVpnIP will attempt to lazily switch to the best remote every
// `PromoteEvery` calls to this function for a given host.
func (hm *HostMap) PromoteBestQueryVpnIP(vpnIp uint32, ifce *Interface) (*HostInfo, error) {
	return hm.queryVpnIP(vpnIp, ifce)
}

func (hm *HostMap) queryVpnIP(vpnIp uint32, promoteIfce *Interface) (*HostInfo, error) {
	if hm.vpnCIDR.Contains(int2ip(vpnIp)) == false && hm.defaultRoute != 0 {
		// FIXME: this shouldn't ship
		d := hm.Hosts[hm.defaultRoute]
		if d != nil {
			return hm.Hosts[hm.defaultRoute], nil
		}
	}
	hm.RLock()
	if h, ok := hm.Hosts[vpnIp]; ok {
		if promoteIfce != nil {
			h.TryPromoteBest(hm.preferredRanges, promoteIfce)
		}
		//fmt.Println(h.remote)
		hm.RUnlock()
		return h, nil
	} else {
		//return &net.UDPAddr{}, nil, errors.New("Unable to find host")
		hm.RUnlock()
		/*
			if lightHouse != nil {
				lightHouse.Query(vpnIp)
				return nil, errors.New("Unable to find host")
			}
		*/
		return nil, errors.New("unable to find host")
	}
}

func (hm *HostMap) CheckHandshakeCompleteIP(vpnIP uint32) bool {
	hm.RLock()
	if i, ok := hm.Hosts[vpnIP]; ok {
		if i == nil {
			hm.RUnlock()
			return false
		}
		complete := i.HandshakeComplete
		hm.RUnlock()
		return complete

	}
	hm.RUnlock()
	return false
}

func (hm *HostMap) CheckHandshakeCompleteIndex(index uint32) bool {
	hm.RLock()
	if i, ok := hm.Indexes[index]; ok {
		if i == nil {
			hm.RUnlock()
			return false
		}
		complete := i.HandshakeComplete
		hm.RUnlock()
		return complete

	}
	hm.RUnlock()
	return false
}

func (hm *HostMap) ClearRemotes(vpnIP uint32) {
	hm.Lock()
	i := hm.Hosts[vpnIP]
	if i == nil {
		hm.Unlock()
		return
	}
	i.ClearRemotes()
	hm.Unlock()
}

func (hm *HostMap) SetDefaultRoute(ip uint32) {
	hm.defaultRoute = ip
}

func (hm *HostMap) PunchList() []*udpAddr {
	var list []*udpAddr
	hm.RLock()
	for _, v := range hm.Hosts {
		for _, r := range v.Remotes {
			list = append(list, r.addr)
		}
		//	if h, ok := hm.Hosts[vpnIp]; ok {
		//		hm.Hosts[vpnIp].PromoteBest(hm.preferredRanges, false)
		//fmt.Println(h.remote)
		//	}
	}
	hm.RUnlock()
	return list
}

func (hm *HostMap) Punchy(conn *udpConn) {
	for {
		for _, addr := range hm.PunchList() {
			conn.WriteTo([]byte{1}, addr)
		}
		time.Sleep(time.Second * 30)
	}
}

func (i *HostInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(m{
		"remote":             i.remote,
		"remotes":            i.Remotes,
		"promote_counter":    i.promoteCounter,
		"connection_state":   i.ConnectionState,
		"handshake_start":    i.handshakeStart,
		"handshake_ready":    i.HandshakeReady,
		"handshake_counter":  i.HandshakeCounter,
		"handshake_complete": i.HandshakeComplete,
		"handshake_packet":   i.HandshakePacket,
		"packet_store":       i.packetStore,
		"remote_index":       i.remoteIndexId,
		"local_index":        i.localIndexId,
		"host_id":            int2ip(i.hostId),
		"receive_errors":     i.recvError,
		"last_roam":          i.lastRoam,
		"last_roam_remote":   i.lastRoamRemote,
	})
}

func (i *HostInfo) BindConnectionState(cs *ConnectionState) {
	i.ConnectionState = cs
}

func (i *HostInfo) TryPromoteBest(preferredRanges []*net.IPNet, ifce *Interface) {
	if i.remote == nil {
		i.ForcePromoteBest(preferredRanges)
		return
	}

	i.promoteCounter++
	if i.promoteCounter%PromoteEvery == 0 {
		// return early if we are already on a preferred remote
		rIP := udp2ip(i.remote.addr)
		for _, l := range preferredRanges {
			if l.Contains(rIP) {
				return
			}
		}

		// We re-query the lighthouse periodically while sending packets, so
		// check for new remotes in our local lighthouse cache
		ips := ifce.lightHouse.QueryCache(i.hostId)
		for _, ip := range ips {
			i.AddRemote(ip)
		}

		best, preferred := i.getBestRemote(preferredRanges)
		if preferred && !best.addr.Equals(i.remote.addr) {
			// Try to send a test packet to that host, this should
			// cause it to detect a roaming event and switch remotes
			ifce.send(test, testRequest, i.ConnectionState, i, best, []byte(""), make([]byte, 12, 12), make([]byte, mtu))
		}
	}
}

func (i *HostInfo) ForcePromoteBest(preferredRanges []*net.IPNet) {
	best, _ := i.getBestRemote(preferredRanges)
	if best != nil {
		i.setRemote(best)
	}
}

func (i *HostInfo) getBestRemote(preferredRanges []*net.IPNet) (best *HostInfoDest, preferred bool) {
	if len(i.Remotes) > 0 {
		for _, r := range i.Remotes {
			rIP := udp2ip(r.addr)

			for _, l := range preferredRanges {
				if l.Contains(rIP) {
					return r, true
				}
			}

			if best == nil || !PrivateIP(rIP) {
				best = r
			}
			/*
				for _, r := range i.Remotes {
					// Must have > 80% probe success to be considered.
					//fmt.Println("GRADE:", r.addr.IP, r.Grade())
					if r.Grade() > float64(.8) {
						if localToMe.Contains(r.addr.IP) == true {
							best = r.addr
							break
							//i.remote = i.Remotes[c].addr
						} else {
								//}
					}
			*/
		}
		return best, false
	}

	return nil, false
}

// rotateRemote will move remote to the next ip in the list of remote ips for this host
// This is different than PromoteBest in that what is algorithmically best may not actually work.
// Only known use case is when sending a stage 0 handshake.
// It may be better to just send stage 0 handshakes to all known ips and sort it out in the receiver.
func (i *HostInfo) rotateRemote() {
	// We have 0, can't rotate
	if len(i.Remotes) < 1 {
		return
	}

	if i.remote == nil {
		i.remote = i.Remotes[0]
		return
	}

	// We want to look at all but the very last entry since that is handled at the end
	for x := 0; x < len(i.Remotes)-1; x++ {
		// Find our current position and move to the next one in the list
		if i.Remotes[x].addr.Equals(i.remote.addr) {
			i.setRemote(i.Remotes[x+1])
			return
		}
	}

	// Our current position was likely the last in the list, start over at 0
	i.setRemote(i.Remotes[0])
}

func (i *HostInfo) cachePacket(t NebulaMessageType, st NebulaMessageSubType, packet []byte, f packetCallback) {
	//TODO: return the error so we can log with more context
	if len(i.packetStore) < 100 {
		tempPacket := make([]byte, len(packet))
		copy(tempPacket, packet)
		//l.WithField("trace", string(debug.Stack())).Error("Caching packet", tempPacket)
		i.packetStore = append(i.packetStore, &cachedPacket{t, st, f, tempPacket})
		l.WithField("vpnIp", IntIp(i.hostId)).
			WithField("length", len(i.packetStore)).
			WithField("stored", true).
			Debugf("Packet store")

	} else if l.Level >= logrus.DebugLevel {
		l.WithField("vpnIp", IntIp(i.hostId)).
			WithField("length", len(i.packetStore)).
			WithField("stored", false).
			Debugf("Packet store")
	}
}

// handshakeComplete will set the connection as ready to communicate, as well as flush any stored packets
func (i *HostInfo) handshakeComplete() {
	//TODO: I'm not certain the distinction between handshake complete and ConnectionState being ready matters because:
	//TODO: HandshakeComplete means send stored packets and ConnectionState.ready means we are ready to send
	//TODO: if the transition from HandhsakeComplete to ConnectionState.ready happens all within this function they are identical

	i.ConnectionState.queueLock.Lock()
	i.HandshakeComplete = true
	//TODO: this should be managed by the handshake state machine to set it based on how many handshake were seen.
	// Clamping it to 2 gets us out of the woods for now
	*i.ConnectionState.messageCounter = 2
	l.WithField("vpnIp", IntIp(i.hostId)).Debugf("Sending %d stored packets", len(i.packetStore))
	nb := make([]byte, 12, 12)
	out := make([]byte, mtu)
	for _, cp := range i.packetStore {
		cp.callback(cp.messageType, cp.messageSubType, i, cp.packet, nb, out)
	}
	i.packetStore = make([]*cachedPacket, 0)
	i.ConnectionState.ready = true
	i.ConnectionState.queueLock.Unlock()
	i.ConnectionState.certState = nil
}

func (i *HostInfo) RemoteUDPAddrs() []*udpAddr {
	var addrs []*udpAddr
	for _, r := range i.Remotes {
		addrs = append(addrs, r.addr)
	}
	return addrs
}

func (i *HostInfo) GetCert() *cert.NebulaCertificate {
	if i.ConnectionState != nil {
		return i.ConnectionState.peerCert
	}
	return nil
}

func (i *HostInfo) AddRemote(r udpAddr) *HostInfoDest {
	remote := &r

	//add := true
	for _, r := range i.Remotes {
		if r.addr.Equals(remote) {
			return r
			//add = false
		}
	}
	// Trim this down if necessary
	if len(i.Remotes) > MaxRemotes {
		i.Remotes = i.Remotes[len(i.Remotes)-MaxRemotes:]
	}
	rd := NewHostInfoDest(remote)
	i.Remotes = append(i.Remotes, rd)
	return rd
	//l.Debugf("Added remote %s for vpn ip", remote)
}

func (i *HostInfo) SetRemote(remote udpAddr) {
	i.setRemote(i.AddRemote(remote))
}

// setRemote should only be called with a reference to an entry inside of the
// i.Remotes map.
//
// External callers should use i.SetRemote
func (i *HostInfo) setRemote(remote *HostInfoDest) {
	i.remote = remote
}

// NOTE: This is only used when the experimental `tun.path_mtu_discovery`
// feature is enabled
func (i *HostInfo) SetRemoteMTU(remoteNoPort *udpAddr, mtu int) {
	for _, r := range i.Remotes {
		if r.addr.IPEquals(remoteNoPort) {
			r.MTUTimestamp = time.Now()
			r.MTU = mtu - NebulaHeaderOverhead
			l.WithField("udpAddr", r.addr).WithField("mtu", mtu).Debug("Updated MTU")
		}
	}
}

func (i *HostInfo) CurrentRemote() *HostInfoDest {
	return i.remote
}

func (i *HostInfo) ClearRemotes() {
	i.remote = nil
	i.Remotes = []*HostInfoDest{}
}

func (i *HostInfo) ClearConnectionState() {
	i.ConnectionState = nil
}

func (i *HostInfo) RecvErrorExceeded() bool {
	if i.recvError < 3 {
		i.recvError += 1
		return false
	}
	return true
}

//########################

func NewHostInfoDest(addr *udpAddr) *HostInfoDest {
	i := &HostInfoDest{
		addr: addr,
	}
	return i
}

func (hid *HostInfoDest) MarshalJSON() ([]byte, error) {
	out := m{
		"address":     hid.addr,
		"probe_count": hid.probeCounter,
	}
	if !hid.MTUTimestamp.IsZero() {
		out["mtu"] = hid.MTU
		out["mtu_timestamp"] = hid.MTUTimestamp.UnixNano() / 1e6
	}
	return json.Marshal(out)
}

// NOTE: This is only used when the experimental `tun.path_mtu_discovery`
// feature is enabled
func (hid *HostInfoDest) GetMTU() int {
	if hid.MTUTimestamp.IsZero() || time.Since(hid.MTUTimestamp) > MTUTimeoutSeconds*time.Second {
		hid.MTUTimestamp = time.Now()

		var err error
		hid.MTU, err = GetKnownMTU(udp2ip(hid.addr))
		if err != nil {
			l.WithField("udpAddr", hid.addr).WithError(err).Error("Failed to lookup current IP_MTU")
		}
		l.WithField("udpAddr", hid.addr).WithField("mtu", hid.MTU).Debug("Lookup Known MTU")
	}
	return hid.MTU
}

/*

func (hm *HostMap) DebugRemotes(vpnIp uint32) string {
	s := "\n"
	for _, h := range hm.Hosts {
		for _, r := range h.Remotes {
			s += fmt.Sprintf("%s : %d ## %v\n", r.addr.IP.String(), r.addr.Port, r.probes)
		}
	}
	return s
}


func (d *HostInfoDest) Grade() float64 {
	c1 := ProbeLen
	for n := len(d.probes) - 1; n >= 0; n-- {
		if d.probes[n] == true {
			c1 -= 1
		}
	}
	return float64(c1) / float64(ProbeLen)
}

func (d *HostInfoDest) Grade() (float64, float64, float64) {
	c1 := ProbeLen
	c2 := ProbeLen / 2
	c2c := ProbeLen - ProbeLen/2
	c3 := ProbeLen / 5
	c3c := ProbeLen - ProbeLen/5
	for n := len(d.probes) - 1; n >= 0; n-- {
		if d.probes[n] == true {
			c1 -= 1
			if n >= c2c {
				c2 -= 1
				if n >= c3c {
					c3 -= 1
				}
			}
		}
		//if n >= d {
	}
	return float64(c3) / float64(ProbeLen/5), float64(c2) / float64(ProbeLen/2), float64(c1) / float64(ProbeLen)
	//return float64(c1) / float64(ProbeLen), float64(c2) / float64(ProbeLen/2), float64(c3) / float64(ProbeLen/5)
}


func (i *HostInfo) HandleReply(addr *net.UDPAddr, counter int) {
	for _, r := range i.Remotes {
		if r.addr.IP.Equal(addr.IP) && r.addr.Port == addr.Port {
			r.ProbeReceived(counter)
		}
	}
}

func (i *HostInfo) Probes() []*Probe {
	p := []*Probe{}
	for _, d := range i.Remotes {
		p = append(p, &Probe{Addr: d.addr, Counter: d.Probe()})
	}
	return p
}


func (d *HostInfoDest) Probe() int {
	//d.probes = append(d.probes, true)
	d.probeCounter++
	d.probes[d.probeCounter%ProbeLen] = true
	return d.probeCounter
	//return d.probeCounter
}

func (d *HostInfoDest) ProbeReceived(probeCount int) {
	if probeCount >= (d.probeCounter - ProbeLen) {
		//fmt.Println("PROBE WORKED", probeCount)
		//fmt.Println(d.addr, d.Grade())
		d.probes[probeCount%ProbeLen] = false
	}
}

*/

// Utility functions

func localIps() *[]net.IP {
	//FIXME: This function is pretty garbage
	var ips []net.IP
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				//continue
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip.To4() != nil && ip.IsLoopback() == false {
				ips = append(ips, ip)
			}
		}
	}
	return &ips
}

func PrivateIP(ip net.IP) bool {
	private := false
	_, private24BitBlock, _ := net.ParseCIDR("10.0.0.0/8")
	_, private20BitBlock, _ := net.ParseCIDR("172.16.0.0/12")
	_, private16BitBlock, _ := net.ParseCIDR("192.168.0.0/16")
	private = private24BitBlock.Contains(ip) || private20BitBlock.Contains(ip) || private16BitBlock.Contains(ip)
	return private
}

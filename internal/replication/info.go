package replication

import (
	"net"
	"strconv"
	"sync"

	"github.com/mrktsm/gedis/internal/server"
)

// InfoProvider combines this node's downstream replication stream with its
// optional upstream replica link. Redis exposes both surfaces in INFO on a
// replica because replicas may themselves have downstream replicas.
type InfoProvider struct {
	mutex   sync.RWMutex
	primary *Primary
	replica *Replica
}

func NewInfoProvider(primary *Primary) *InfoProvider {
	return &InfoProvider{primary: primary}
}

func (p *InfoProvider) SetReplica(replica *Replica) {
	p.mutex.Lock()
	p.replica = replica
	p.mutex.Unlock()
}

func (p *InfoProvider) ReplicationInfo() server.ReplicationInfo {
	p.mutex.RLock()
	primary := p.primary
	replica := p.replica
	p.mutex.RUnlock()

	info := server.ReplicationInfo{Role: "master"}
	if primary != nil {
		stats := primary.Stats()
		info.ReplicationID = stats.ReplicationID
		info.Offset = stats.Offset
		info.ConnectedReplicas = stats.ConnectedReplicas
		info.BacklogActive = true
		info.BacklogSize = stats.BacklogCapacity
		info.BacklogFirstByteOffset = stats.BacklogFirstByte
		info.BacklogHistoryLength = stats.BacklogBytes
	}
	if replica == nil {
		return info
	}

	stats := replica.Stats()
	info.Role = "slave"
	info.PrimaryHost, info.PrimaryPort = splitPrimaryAddress(stats.PrimaryAddress)
	info.PrimaryLinkUp = stats.Connected
	info.PrimarySyncInProgress = stats.Syncing
	info.ReplicaReadOffset = stats.Offset
	info.ReplicaAppliedOffset = stats.Offset
	info.ReplicaReadOnly = true
	info.UpstreamReplicationID = stats.ReplicationID
	info.FullSyncs = stats.FullSyncs
	info.PartialSyncs = stats.PartialSyncs
	info.Reconnects = stats.Reconnects
	info.LastError = stats.LastError
	return info
}

func splitPrimaryAddress(address string) (string, int) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address, 0
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil {
		return host, 0
	}
	return host, parsedPort
}

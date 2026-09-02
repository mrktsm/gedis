package replication

import (
	"testing"

	"github.com/mrktsm/gedis/internal/server"
)

func TestInfoProviderCombinesDownstreamAndUpstreamState(t *testing.T) {
	t.Parallel()

	primary, err := NewPrimary(PrimaryConfig{
		BacklogBytes:  128,
		ReplicationID: testReplicationID,
	})
	if err != nil {
		t.Fatalf("NewPrimary() error = %v", err)
	}
	if err := primary.Append([][]byte{[]byte("SET"), []byte("key"), []byte("value")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	subscription, _ := primary.Subscribe()
	defer subscription.Close()

	const upstreamID = "abcdef0123456789abcdef0123456789abcdef01"
	replica, err := NewReplica(ReplicaConfig{
		PrimaryAddress: "primary.example:6380",
		InitialState: &PersistentState{
			Version:        1,
			PrimaryAddress: "primary.example:6380",
			ReplicationID:  upstreamID,
			Offset:         99,
		},
	}, server.NewEngine())
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	replica.mutex.Lock()
	replica.stats.Connected = true
	replica.stats.FullSyncs = 1
	replica.stats.PartialSyncs = 2
	replica.stats.Reconnects = 3
	replica.mutex.Unlock()

	provider := NewInfoProvider(primary)
	provider.SetReplica(replica)
	got := provider.ReplicationInfo()
	if got.Role != "slave" || got.PrimaryHost != "primary.example" || got.PrimaryPort != 6380 || !got.PrimaryLinkUp {
		t.Fatalf("upstream info = %#v", got)
	}
	if got.ReplicationID != testReplicationID || got.Offset <= 0 || got.ConnectedReplicas != 1 {
		t.Fatalf("downstream info = %#v", got)
	}
	if got.BacklogSize != 128 || !got.BacklogActive || got.BacklogHistoryLength <= 0 {
		t.Fatalf("backlog info = %#v", got)
	}
	if got.UpstreamReplicationID != upstreamID || got.ReplicaAppliedOffset != 99 {
		t.Fatalf("replica offsets = %#v", got)
	}
	if got.FullSyncs != 1 || got.PartialSyncs != 2 || got.Reconnects != 3 {
		t.Fatalf("replica counters = %#v", got)
	}
}

package p2p

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/p2p/common"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	testRecentPeerOne     = "/ip4/203.0.113.1/tcp/8150/p2p/16Uiu2HAmDynYpWjWmgVGf9qVWvDdLnJ3ybVgDmFexizR4zMereus"
	testRecentPeerTwo     = "/ip4/203.0.113.2/tcp/8150/p2p/16Uiu2HAm9UcTayJDSajjJYsWwVaN2qqGeczcs9kXse3dMdvGDRjz"
	testRecentPeerBad     = "/ip4/203.0.113.3/tcp/8150"
	testRecentPeerPrivate = "/ip4/10.0.0.4/tcp/8150/p2p/16Uiu2HAkvH1U2kQqQFh6fSVQPGgABaKNHRMT7DceGe6kRtYr6ndY"
)

func TestRecentPeerAddrsFromCacheFiltersAndSorts(t *testing.T) {
	now := time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC)
	self, err := peer.Decode("16Uiu2HAmDynYpWjWmgVGf9qVWvDdLnJ3ybVgDmFexizR4zMereus")
	if err != nil {
		t.Fatal(err)
	}
	cache := recentPeerCache{
		Version:   recentPeerCacheVersion,
		UpdatedAt: now.Format(time.RFC3339Nano),
		Peers: []recentPeerRecord{
			{
				Addr:     testRecentPeerBad,
				LastSeen: now.Format(time.RFC3339Nano),
			},
			{
				Addr:     testRecentPeerOne,
				LastSeen: now.Format(time.RFC3339Nano),
			},
			{
				Addr:     testRecentPeerTwo,
				LastSeen: now.Add(-time.Hour).Format(time.RFC3339Nano),
			},
			{
				Addr:     "/ip4/203.0.113.4/tcp/8150/p2p/16Uiu2HAkvH1U2kQqQFh6fSVQPGgABaKNHRMT7DceGe6kRtYr6ndY",
				LastSeen: now.Add(-recentPeerMaxAge - time.Second).Format(time.RFC3339Nano),
			},
			{
				Addr:     testRecentPeerPrivate,
				LastSeen: now.Format(time.RFC3339Nano),
			},
		},
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}

	addrs := recentPeerAddrsFromCache(data, self, now)
	if len(addrs) != 1 {
		t.Fatalf("recent peer addrs = %v, want only non-self fresh complete peer", addrs)
	}
	if addrs[0] != testRecentPeerTwo {
		t.Fatalf("recent peer addr = %s, want %s", addrs[0], testRecentPeerTwo)
	}
}

func TestMergeRecentPeerRecordsUpdatesAndLimits(t *testing.T) {
	now := time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour)
	records := []recentPeerRecord{
		{
			Addr:     testRecentPeerOne,
			LastSeen: old.Format(time.RFC3339Nano),
			Source:   "old",
		},
	}

	merged := mergeRecentPeerRecords(records, []string{testRecentPeerOne, testRecentPeerTwo, testRecentPeerBad, testRecentPeerPrivate}, "connect", "", now)
	if len(merged) != 2 {
		t.Fatalf("merged records length = %d, want 2: %#v", len(merged), merged)
	}
	if merged[0].Addr != testRecentPeerOne || merged[0].Source != "connect" || merged[0].LastSeen != now.Format(time.RFC3339Nano) {
		t.Fatalf("first merged record not refreshed newest peer: %#v", merged[0])
	}
	if merged[1].Addr != testRecentPeerTwo {
		t.Fatalf("second merged record = %s, want %s", merged[1].Addr, testRecentPeerTwo)
	}
}

func TestNormalizeRecentPeerAddrRejectsPrivateAndLocalAddresses(t *testing.T) {
	for _, addr := range []string{
		testRecentPeerPrivate,
		"/ip4/172.18.0.3/tcp/8150/p2p/16Uiu2HAmQSJzJjXUxtyX5rc2bQBAsTvhcjp4GQBPmwqEYu9D8zA5",
		"/ip4/127.0.0.1/tcp/8150/p2p/16Uiu2HAmQSJzJjXUxtyX5rc2bQBAsTvhcjp4GQBPmwqEYu9D8zA5",
		"/ip4/169.254.1.1/tcp/8150/p2p/16Uiu2HAmQSJzJjXUxtyX5rc2bQBAsTvhcjp4GQBPmwqEYu9D8zA5",
	} {
		if normalized, ok := normalizeRecentPeerAddr(addr, ""); ok {
			t.Fatalf("normalizeRecentPeerAddr(%q) = %q, want rejected", addr, normalized)
		}
	}
}

func TestMergePeerAddrStringsDedupes(t *testing.T) {
	merged := mergePeerAddrStrings(
		[]string{" a ", "b"},
		[]string{"a", "", "c"},
	)
	want := []string{"a", "b", "c"}
	if len(merged) != len(want) {
		t.Fatalf("merged = %v, want %v", merged, want)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Fatalf("merged = %v, want %v", merged, want)
		}
	}
}

func TestStaticPeersOnlyDisablesRecentPeerCache(t *testing.T) {
	service := &Service{
		cfg: &common.Config{
			StaticPeersOnly: true,
			DataDir:         t.TempDir(),
		},
	}
	if path := service.recentPeerCachePath(); path != "" {
		t.Fatalf("static-only recent peer cache path = %q, want disabled", path)
	}
}

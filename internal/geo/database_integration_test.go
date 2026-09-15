//go:build integration

package geo

import (
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
)

func TestRealDatabaseConcurrentLookup(t *testing.T) {
	db, err := Open(filepath.Join("..", "..", "data", "ip2region_v4.xdb"), filepath.Join("..", "..", "data", "ip2region_v6.xdb"))
	if err != nil {
		t.Fatalf("load databases (run make db first): %v", err)
	}
	addresses := []string{"114.114.114.114", "8.8.8.8", "1.1.1.1", "2001:4860:4860::8888", "240e:3b7:3272:d8d0:db09:c067:8d59:539e"}
	baselines := make([]Location, len(addresses))
	for i, address := range addresses {
		baselines[i], err = db.Lookup(netip.MustParseAddr(address))
		if err != nil || baselines[i].Country == "" {
			t.Fatalf("lookup %s: %+v, %v", address, baselines[i], err)
		}
		t.Logf("%s: %+v", address, baselines[i])
	}
	if baselines[0].CountryCode != "CN" || baselines[1].CountryCode != "US" || baselines[3].CountryCode != "US" {
		t.Fatalf("unexpected country mapping: %+v", baselines)
	}
	mapped, err := db.Lookup(netip.MustParseAddr("::ffff:114.114.114.114"))
	if err != nil || mapped != baselines[0] {
		t.Fatalf("IPv4-mapped IPv6 lookup: %+v, %v", mapped, err)
	}
	for _, address := range []string{"0.0.0.0", "255.255.255.255", "::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"} {
		if _, err := db.Lookup(netip.MustParseAddr(address)); err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatalf("boundary address %s: %v", address, err)
		}
	}
	if _, err := db.Lookup(netip.IPv6Unspecified()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unspecified IPv6 should have no location in the pinned database: %v", err)
	}
	var group sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		group.Go(func() {
			for i := 0; i < 100; i++ {
				index := i % len(addresses)
				got, err := db.Lookup(netip.MustParseAddr(addresses[index]))
				if err != nil || got != baselines[index] {
					t.Errorf("concurrent lookup %s: %+v, %v", addresses[index], got, err)
					return
				}
			}
		})
	}
	group.Wait()
}

func TestRejectsSwappedDatabaseVersions(t *testing.T) {
	_, err := Open(filepath.Join("..", "..", "data", "ip2region_v6.xdb"), filepath.Join("..", "..", "data", "ip2region_v4.xdb"))
	if err == nil {
		t.Fatal("accepted IPv6 data as an IPv4 database")
	}
}

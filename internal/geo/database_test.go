package geo

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

func TestParseLocation(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want Location
	}{
		{"中国|广东省|深圳市|电信|CN", Location{"中国", "广东省", "深圳市", "电信", "CN"}},
		{"Australia|Queensland|Brisbane|0|AU", Location{"Australia", "Queensland", "Brisbane", "", "AU"}},
		{"0|0|0|内网IP|0", Location{"", "", "", "内网IP", ""}},
	} {
		got, err := parseLocation(test.raw)
		if err != nil || got != test.want {
			t.Fatalf("parseLocation(%q) = %+v, %v; want %+v", test.raw, got, err, test.want)
		}
	}
	if _, err := parseLocation(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty region: %v", err)
	}
	for _, raw := range []string{"invalid", "a|b|c|d", "a|b|c|d|e|f"} {
		if _, err := parseLocation(raw); err == nil {
			t.Fatalf("accepted invalid schema %q", raw)
		}
	}
}

func TestOpenRejectsMissingAndInvalidFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.xdb")
	if _, err := Open(missing, missing); err == nil {
		t.Fatal("accepted missing database")
	}
	bad := filepath.Join(t.TempDir(), "bad.xdb")
	if err := os.WriteFile(bad, []byte("not an xdb"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bad, bad); err == nil {
		t.Fatal("accepted invalid database")
	}
}

func TestRejectsOutOfBoundsDatabase(t *testing.T) {
	const start = xdb.HeaderInfoLength + xdb.VectorIndexRows*xdb.VectorIndexCols*xdb.VectorIndexSize
	for _, test := range []struct {
		name   string
		mutate func([]byte, *xdb.Header)
	}{
		{"truncated index", func(_ []byte, header *xdb.Header) { header.EndIndexPtr++ }},
		{"bad vector", func(content []byte, _ *xdb.Header) {
			binary.LittleEndian.PutUint32(content[xdb.HeaderInfoLength:], 1)
		}},
		{"out of bounds region", func(content []byte, _ *xdb.Header) {
			binary.LittleEndian.PutUint16(content[start+8:], 100)
			binary.LittleEndian.PutUint32(content[start+10:], uint32(len(content)))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := make([]byte, start+xdb.IPv4.SegmentIndexSize)
			header := &xdb.Header{IndexPolicy: xdb.VectorIndexPolicy, StartIndexPtr: start, EndIndexPtr: start}
			test.mutate(content, header)
			if err := validateContent(content, header, xdb.IPv4); err == nil {
				t.Fatal("accepted a corrupted database")
			}
		})
	}
}

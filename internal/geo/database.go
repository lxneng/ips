package geo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/lionsoul2014/ip2region/binding/golang/service"
	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

var ErrNotFound = errors.New("IP address not found")

type Location struct {
	Country     string `json:"country"`
	Province    string `json:"province"`
	City        string `json:"city"`
	ISP         string `json:"isp"`
	CountryCode string `json:"country_code"`
}

// Database owns immutable snapshots; replacing files requires restarting the service.
type Database struct {
	v4 *memoryDB
	v6 *memoryDB
}

type memoryDB struct {
	version *xdb.Version
	content []byte
}

func Open(v4Path, v6Path string) (*Database, error) {
	v4Config, err := service.NewV4Config(service.BufferCache, v4Path, 1)
	if err != nil {
		return nil, fmt.Errorf("load IPv4 database: %w", err)
	}
	v4, err := newMemoryDB(v4Config)
	if err != nil {
		return nil, fmt.Errorf("validate IPv4 database: %w", err)
	}
	v6Config, err := service.NewV6Config(service.BufferCache, v6Path, 1)
	if err != nil {
		return nil, fmt.Errorf("load IPv6 database: %w", err)
	}
	v6, err := newMemoryDB(v6Config)
	if err != nil {
		return nil, fmt.Errorf("validate IPv6 database: %w", err)
	}
	return &Database{v4: v4, v6: v6}, nil
}

func newMemoryDB(config *service.Config) (*memoryDB, error) {
	content, header, version := config.CBuffer(), config.Header(), config.IPVersion()
	if err := validateContent(content, header, version); err != nil {
		return nil, err
	}
	return &memoryDB{version: version, content: content}, nil
}

// The upstream verifier checks the header but not index/data bounds.
func validateContent(content []byte, header *xdb.Header, version *xdb.Version) error {
	const indexEnd = xdb.HeaderInfoLength + xdb.VectorIndexRows*xdb.VectorIndexCols*xdb.VectorIndexSize
	start, end, size := uint64(header.StartIndexPtr), uint64(header.EndIndexPtr), uint64(len(content))
	segmentSize := uint64(version.SegmentIndexSize)
	if header.IndexPolicy != xdb.VectorIndexPolicy || size < indexEnd || start < indexEnd ||
		end < start || end+segmentSize > size || (end-start)%segmentSize != 0 {
		return errors.New("invalid or truncated xdb segment index")
	}
	for offset := xdb.HeaderInfoLength; offset < indexEnd; offset += xdb.VectorIndexSize {
		first := uint64(binary.LittleEndian.Uint32(content[offset:]))
		last := uint64(binary.LittleEndian.Uint32(content[offset+4:]))
		if first == 0 && last == 0 {
			continue
		}
		// The official writer uses a one-past-the-end sentinel for the final vector.
		if offset == indexEnd-xdb.VectorIndexSize && first == end && last == end+segmentSize {
			last = end
		}
		if first < start || last < first || last > end || (first-start)%segmentSize != 0 || (last-start)%segmentSize != 0 {
			return fmt.Errorf("invalid xdb vector index at %d", offset)
		}
	}
	for offset := start; offset <= end; offset += segmentSize {
		dataOffset := offset + uint64(version.Bytes*2)
		length := uint64(binary.LittleEndian.Uint16(content[dataOffset:]))
		pointer := uint64(binary.LittleEndian.Uint32(content[dataOffset+2:]))
		if pointer+length > size {
			return fmt.Errorf("invalid xdb data pointer at %d", offset)
		}
	}
	return nil
}

func (db *Database) Lookup(address netip.Addr) (Location, error) {
	if !address.IsValid() || address.Zone() != "" {
		return Location{}, errors.New("invalid IP address")
	}
	address = address.Unmap()
	reader := db.v6
	if address.Is4() {
		reader = db.v4
	}
	raw, err := reader.search(address.AsSlice())
	if err != nil {
		return Location{}, fmt.Errorf("search xdb: %w", err)
	}
	return parseLocation(raw)
}

func (db *memoryDB) search(ip []byte) (string, error) {
	searcher, err := xdb.NewWithBuffer(db.version, db.content)
	if err != nil {
		return "", err
	}
	return searcher.Search(ip)
}

func parseLocation(raw string) (Location, error) {
	if raw == "" {
		return Location{}, ErrNotFound
	}
	fields := strings.Split(raw, "|")
	if len(fields) != 5 {
		return Location{}, fmt.Errorf("unsupported region schema: expected 5 fields, got %d", len(fields))
	}
	for i := range fields {
		if fields[i] == "0" {
			fields[i] = ""
		}
	}
	return Location{Country: fields[0], Province: fields[1], City: fields[2], ISP: fields[3], CountryCode: fields[4]}, nil
}

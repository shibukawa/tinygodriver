//go:build !tinygo

package s3

import (
	"strconv"
	"strings"
	"testing"
)

// A listing page is the largest document this package parses, so the scanner's
// cost is measured against a page of realistic size: 200 keys, the metadata S3
// sends for each, and a handful of common prefixes.

func buildBenchListing(keys int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	b.WriteString(`<Name>bench-bucket</Name><Prefix>logs/</Prefix>`)
	b.WriteString(`<KeyCount>` + strconv.Itoa(keys) + `</KeyCount>`)
	b.WriteString(`<MaxKeys>1000</MaxKeys><IsTruncated>true</IsTruncated>`)
	b.WriteString(`<NextContinuationToken>1ueGcxLPRx1Tr/XYExHnhbYLgveDs2J/wm36Hy4vbOwM=</NextContinuationToken>`)
	for i := 0; i < keys; i++ {
		n := strconv.Itoa(i)
		b.WriteString(`<Contents>`)
		b.WriteString(`<Key>logs/2026/08/01/sensor-reading-` + n + `.json</Key>`)
		b.WriteString(`<LastModified>2026-08-01T12:30:` + strconv.Itoa(i%60/10) + strconv.Itoa(i%10) + `.000Z</LastModified>`)
		b.WriteString(`<ETag>&quot;9b2cf535f27731c974343645a3985328&quot;</ETag>`)
		b.WriteString(`<Size>` + strconv.Itoa(1024+i) + `</Size>`)
		b.WriteString(`<StorageClass>STANDARD</StorageClass>`)
		b.WriteString(`</Contents>`)
	}
	for i := 0; i < 5; i++ {
		b.WriteString(`<CommonPrefixes><Prefix>logs/2026/08/0` + strconv.Itoa(i+2) + `/</Prefix></CommonPrefixes>`)
	}
	b.WriteString(`</ListBucketResult>`)
	return []byte(b.String())
}

var benchListingDoc = buildBenchListing(200)

func BenchmarkParseListBucketResult(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchListingDoc)))
	for i := 0; i < b.N; i++ {
		result, err := parseListBucketResult(benchListingDoc)
		if err != nil {
			b.Fatal(err)
		}
		if len(result.Objects) != 200 {
			b.Fatalf("parsed %d objects, want 200", len(result.Objects))
		}
	}
}

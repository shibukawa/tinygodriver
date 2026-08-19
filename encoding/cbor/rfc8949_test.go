package cbor

import (
	"encoding/hex"
	"testing"
)

func TestRFC8949AppendixAValidVectors(t *testing.T) {
	vectors := []string{
		"00", "01", "0a", "17", "1818", "1819", "1864", "1903e8",
		"1a000f4240", "1b000000e8d4a51000", "1bffffffffffffffff",
		"20", "29", "3863", "3903e7", "3bffffffffffffffff",
		"f90000", "f98000", "f93c00", "fb3ff199999999999a",
		"f4", "f5", "f6", "40", "4401020304", "60", "6161",
		"6449455446", "80", "83010203", "9f018202039f0405ffff",
		"a0", "a201020304", "bf61610161629f0203ffff",
		"c074323031332d30332d32315432303a30343a30305a",
	}
	for _, vector := range vectors {
		data, err := hex.DecodeString(vector)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(data, DecoderOptions{RejectDuplicateMapKeys: true}); err != nil {
			t.Errorf("%s: %v", vector, err)
		}
	}
}

func TestIndefiniteContainerLimit(t *testing.T) {
	data, _ := hex.DecodeString("9f0001ff")
	if err := Validate(data, DecoderOptions{MaxContainerItems: 1}); err == nil {
		t.Fatal("accepted oversized indefinite array")
	}
	data, _ = hex.DecodeString("bf00010002ff")
	if err := Validate(data, DecoderOptions{MaxContainerItems: 1}); err == nil {
		t.Fatal("accepted oversized indefinite map")
	}
}

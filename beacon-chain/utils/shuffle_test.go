package utils

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/prysmaticlabs/prysm/shared/params"
)

func TestShuffleIndices_InvalidValidatorCount(t *testing.T) {
	var list []uint64

	upperBound := 1<<(params.BeaconConfig().RandBytes*8) - 1

	for i := 0; i < upperBound+1; i++ {
		list = append(list, uint64(i))
	}

	if _, err := ShuffleIndices(common.Hash{'a'}, list); err == nil {
		t.Error("Shuffle should have failed when validator count exceeds ModuloBias")
	}
}

func TestShuffleIndices_OK(t *testing.T) {
	hash1 := common.BytesToHash([]byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'a', 'b', 'c', 'd', 'e', 'f', 'g'})
	hash2 := common.BytesToHash([]byte{'1', '2', '3', '4', '5', '6', '7', '1', '2', '3', '4', '5', '6', '7', '1', '2', '3', '4', '5', '6', '7', '1', '2', '3', '4', '5', '6', '7', '1', '2', '3', '4', '5', '6', '7'})
	var list1 []uint64

	for i := 0; i < 10; i++ {
		list1 = append(list1, uint64(i))
	}

	list2 := make([]uint64, len(list1))
	copy(list2, list1)

	list1, err := ShuffleIndices(hash1, list1)
	if err != nil {
		t.Errorf("Shuffle failed with: %v", err)
	}

	list2, err = ShuffleIndices(hash2, list2)
	if err != nil {
		t.Errorf("Shuffle failed with: %v", err)
	}

	if reflect.DeepEqual(list1, list2) {
		t.Errorf("2 shuffled lists shouldn't be equal")
	}
	if !reflect.DeepEqual(list1, []uint64{5, 4, 9, 6, 7, 3, 0, 1, 8, 2}) {
		t.Errorf("list 1 was incorrectly shuffled")
	}
	if !reflect.DeepEqual(list2, []uint64{9, 0, 1, 5, 3, 2, 4, 7, 8, 6}) {
		t.Errorf("list 2 was incorrectly shuffled")
	}
}

func TestSplitIndices_OK(t *testing.T) {
	var l []uint64
	validators := 64000
	for i := 0; i < validators; i++ {
		l = append(l, uint64(i))
	}
	split := SplitIndices(l, params.BeaconConfig().SlotsPerEpoch)
	if len(split) != int(params.BeaconConfig().SlotsPerEpoch) {
		t.Errorf("Split list failed due to incorrect length, wanted:%v, got:%v", params.BeaconConfig().SlotsPerEpoch, len(split))
	}

	for _, s := range split {
		if len(s) != validators/int(params.BeaconConfig().SlotsPerEpoch) {
			t.Errorf("Split list failed due to incorrect length, wanted:%v, got:%v", validators/int(params.BeaconConfig().SlotsPerEpoch), len(s))
		}
	}
}

func BenchmarkShuffledIndex(b *testing.B) {
	listSizes := []uint64{4000000, 40000, 400}
	// Random 32 bytes seed for testing.
	seed := [32]byte{123, 42}
	for _, listSize := range listSizes {
		b.Run(fmt.Sprintf("ShuffledIndex_%d", listSize), func(ib *testing.B) {
			for i := uint64(0); i < uint64(ib.N); i++ {
				ShuffledIndex(i%listSize, listSize, seed)
			}
		})
	}
}

func TestSplitIndicesAndOffset_OK(t *testing.T) {
	var l []uint64
	validators := uint64(64000)
	for i := uint64(0); i < validators; i++ {
		l = append(l, i)
	}
	chunks := uint64(6)
	split := SplitIndices(l, chunks)
	for i := uint64(0); i < chunks; i++ {
		if !reflect.DeepEqual(split[i], l[SplitOffset(uint64(len(l)), chunks, i):SplitOffset(uint64(len(l)), chunks, i+1)]) {
			t.Errorf("Want: %v got: %v", l[SplitOffset(uint64(len(l)), chunks, i):SplitOffset(uint64(len(l)), chunks, i+1)], split[i])
			break
		}

	}
}

func TestSplitOffset_OK(t *testing.T) {
	testCases := []struct {
		listSize uint64
		chunks   uint64
		index    uint64
		offset   uint64
	}{
		{30, 3, 2, 20},
		{1000, 10, 60, 6000},
		{2482, 10, 70, 17374},
		{323, 98, 56, 184},
		{273, 8, 6, 204},
		{3274, 98, 256, 8552},
		{23, 3, 2, 15},
		{23, 3, 9, 69},
	}
	for _, tt := range testCases {
		result := SplitOffset(tt.listSize, tt.chunks, tt.index)
		if result != tt.offset {
			t.Errorf("got %d, want %d", result, tt.offset)
		}

	}

}

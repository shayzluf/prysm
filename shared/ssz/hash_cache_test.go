package ssz

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
)

type JunkObject struct {
	D2Int64Slice [][]uint64
	Uint         uint64
	Int64Slice   []uint64
}

// GenerateJunkObject generates junk object.
func GenerateJunkObject(size uint64) []*JunkObject {
	object := make([]*JunkObject, size)
	for i := uint64(0); i < uint64(len(object)); i++ {
		d2Int64Slice := make([][]uint64, size)
		is := make([]uint64, size)
		uInt := uint64(time.Now().UnixNano())
		is[i] = i
		d2Int64Slice[i] = make([]uint64, size)
		for j := uint64(0); j < uint64(len(object)); j++ {
			d2Int64Slice[i][j] = i + j
		}
		object[i] = &JunkObject{
			D2Int64Slice: d2Int64Slice,
			Uint:         uInt,
			Int64Slice:   is,
		}

	}
	return object
}

func TestHashKeyFn_OK(t *testing.T) {
	mRoot := &root{
		Hash: common.HexToHash("0x0123456"),
	}

	key, err := hashKeyFn(mRoot)
	if err != nil {
		t.Fatal(err)
	}
	if key != mRoot.Hash.Hex() {
		t.Errorf("Incorrect hash key: %s, expected %s", key, mRoot.Hash.Hex())
	}
}

func TestHashKeyFn_InvalidObj(t *testing.T) {
	_, err := hashKeyFn("bad")
	if err != ErrNotMerkleRoot {
		t.Errorf("Expected error %v, got %v", ErrNotMerkleRoot, err)
	}
}

func TestObjCache_byHash(t *testing.T) {
	cache := newHashCache()

	byteSl := [][]byte{{0, 0}, {1, 1}}
	mr, err := merkleHash(byteSl)
	if err != nil {
		t.Fatal(err)
	}
	hs, err := hashedEncoding(reflect.ValueOf(byteSl))
	if err != nil {
		t.Fatal(err)
	}
	exists, _, err := cache.RootByEncodedHash(bytesutil.ToBytes32(hs))

	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("Expected block info not to exist in empty cache")
	}

	if _, err := cache.MerkleHashCached(byteSl); err != nil {
		t.Fatal(err)
	}

	exists, fetchedInfo, err := cache.RootByEncodedHash(bytesutil.ToBytes32(hs))

	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("Expected blockInfo to exist")
	}
	if bytes.Compare(mr, fetchedInfo.MarkleRoot) != 0 {
		t.Errorf(
			"Expected fetched info number to be %v, got %v",
			mr,
			fetchedInfo.MarkleRoot,
		)
	}
	if fetchedInfo.Hash != bytesutil.ToBytes32(hs) {
		t.Errorf(
			"Expected fetched info hash to be %v, got %v",
			hs,
			fetchedInfo.Hash,
		)
	}
}

func TestMerkleHashWithCache(t *testing.T) {
	cache := newHashCache()
	for i := 0; i < 200; i++ {

		runMerkleHashTests(t, func(val [][]byte) ([]byte, error) {
			return merkleHash(val)
		})

	}

	for i := 0; i < 200; i++ {

		runMerkleHashTests(t, func(val [][]byte) ([]byte, error) {
			return cache.MerkleHashCached(val)
		})

	}

}

func BenchmarkHashWithoutCache(b *testing.B) {
	useCache = false
	First := GenerateJunkObject(100)
	type tree struct {
		First  []*JunkObject
		Second []*JunkObject
	}
	TreeHash(&tree{First: First, Second: First})
	for n := 0; n < b.N; n++ {
		TreeHash(&tree{First: First, Second: First})
	}
}

func BenchmarkHashWithCache(b *testing.B) {
	useCache = true
	First := GenerateJunkObject(100)
	type tree struct {
		First  []*JunkObject
		Second []*JunkObject
	}
	TreeHash(&tree{First: First, Second: First})
	for n := 0; n < b.N; n++ {
		TreeHash(&tree{First: First, Second: First})
	}
}

func TestBlockCache_maxSize(t *testing.T) {
	cache := newHashCache()
	maxCacheSize = 10000
	for i := uint64(0); i < uint64(maxCacheSize+10); i++ {

		if err := cache.AddRoot(bytesutil.ToBytes32(bytesutil.Bytes4(i)), []byte{1}); err != nil {
			t.Fatal(err)
		}
	}

	if len(cache.hashCache.ListKeys()) != maxCacheSize {
		t.Errorf(
			"Expected hash cache key size to be %d, got %d",
			maxCacheSize,
			len(cache.hashCache.ListKeys()),
		)
	}
}

func TestBlockCache_prune_removeLastRead(t *testing.T) {
	cache := newHashCache()
	maxCacheSize = 10000
	for i := uint64(0); i < uint64(maxCacheSize); i++ {

		if err := cache.AddRoot(bytesutil.ToBytes32(bytesutil.Bytes4(i)), []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	exists, _, err := cache.RootByEncodedHash(bytesutil.ToBytes32(bytesutil.Bytes4(0)))
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("Expected blockInfo to exist")
	}
	if err := cache.AddRoot(bytesutil.ToBytes32(bytesutil.Bytes4(10000)), []byte{1}); err != nil {
		t.Fatal(err)
	}
	exists, _, err = cache.RootByEncodedHash(bytesutil.ToBytes32(bytesutil.Bytes4(0)))
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("Expected blockInfo to exist")
	}
	exists, _, err = cache.RootByEncodedHash(bytesutil.ToBytes32(bytesutil.Bytes4(1)))
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("Expected blockInfo to be pruned")
	}
	if len(cache.hashCache.ListKeys()) != maxCacheSize {
		t.Errorf(
			"Expected hash cache key size to be %d, got %d",
			maxCacheSize,
			len(cache.hashCache.ListKeys()),
		)
	}
}

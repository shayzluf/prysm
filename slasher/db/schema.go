package db

import (
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
)

// The Schema will define how to store and retrieve data from the db.
// Currently we store blocks by prefixing `block` to their hash and
// using that as the key to store blocks.
// `block` + hash -> block
//
// We store the state using the state lookup key, and
// also the genesis block using the genesis lookup key.
// The canonical head is stored using the canonical head lookup key.

// The fields below define the suffix of keys in the db.
var (

	// Slasher
	historicIndexedAttestationsBucket = []byte("historic-indexed-attestations-bucket")
	indexedAttestationsIndicesBucket  = []byte("indexed-attestations-indices-bucket")

	historicBlockHeadersBucket = []byte("historic-block-headers-bucket")

	// DB internal use
	cleanupHistoryBucket = []byte("cleanup-history-bucket")
)

func encodeSlotNumberRoot(number uint64, root [32]byte) []byte {
	return append(bytesutil.Bytes8(number), root[:]...)
}

func encodeEpochValidatorID(epoch uint64, validatorID uint64) []byte {
	return append(bytesutil.Bytes8(epoch), bytesutil.Bytes8(validatorID)...)
}

func encodeEpochValidatorIDSig(epoch uint64, validatorID uint64, sig []byte) []byte {
	return append(append(bytesutil.Bytes8(epoch), bytesutil.Bytes8(validatorID)...), sig...)
}

func encodeEpochSig(epoch uint64, sig []byte) []byte {
	return append(bytesutil.Bytes8(epoch), sig...)
}

// encodeSlotNumber encodes a slot number as little-endian uint32.
func encodeSlotNumber(number uint64) []byte {
	return bytesutil.Bytes8(number)
}

// decodeSlotNumber returns a slot number which has been
// encoded as a little-endian uint32 in the byte array.
func decodeToSlotNumber(bytearray []byte) uint64 {
	return bytesutil.FromBytes8(bytearray)
}
